// account-server 是 tomato-platform 的统一身份与客户平台入口。
//
// 第一阶段提供 Sign in with Apple、平台 Token 生命周期和 PostgreSQL 会话；
// 账号删除与各业务数据迁移会在后续阶段沿这个边界继续实现。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"tomato-platform/internal/account"
	"tomato-platform/internal/observability"
	"tomato-platform/internal/platformdb"
	"tomato-platform/internal/platformpurge"
)

type databasePinger interface {
	Ping(context.Context) error
}

type schemaChecker interface {
	Ready(context.Context) error
}

type server struct {
	db      databasePinger
	schema  schemaChecker
	account *account.Service
}

func main() {
	_ = godotenv.Load()
	observability.Configure("account-server")
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// 与现有业务进程保持一致：本地开发可从仓库根目录的 .env 读取，
	// 已由部署环境导出的变量不会被覆盖。
	tokens, err := account.NewTokenManager(os.Getenv("ACCOUNT_TOKEN_SECRET"))
	if err != nil {
		return err
	}
	appleVerifier, err := account.NewAppleVerifier(strings.Split(os.Getenv("APPLE_CLIENT_IDS"), ","))
	if err != nil {
		return err
	}
	dataCipher, err := account.NewTokenCipher(os.Getenv("ACCOUNT_DATA_KEY"))
	if err != nil {
		return err
	}
	privateKeyPath := strings.TrimSpace(os.Getenv("APPLE_PRIVATE_KEY_PATH"))
	if privateKeyPath == "" {
		return errors.New("APPLE_PRIVATE_KEY_PATH 未设置")
	}
	privateKeyPEM, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return fmt.Errorf("读取 Apple 私钥失败: %w", err)
	}
	appleTokens, err := account.NewAppleOAuthClient(
		os.Getenv("APPLE_TEAM_ID"), os.Getenv("APPLE_KEY_ID"), privateKeyPEM,
	)
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := platformdb.Open(ctx, os.Getenv("PLATFORM_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()

	accountStore := account.NewPostgresStore(pool)
	accountService := account.NewService(accountStore, appleVerifier, appleTokens, dataCipher, tokens)
	purgers, err := productPurgers(os.Getenv("PLATFORM_INTERNAL_TOKEN"))
	if err != nil {
		return err
	}
	deletionWorker := account.NewDeletionWorker(accountStore, appleTokens, dataCipher, purgers...)
	srv := &server{db: pool, schema: accountStore, account: accountService}
	httpServer := &http.Server{
		Addr:              ":" + envOr("ACCOUNT_PORT", "8460"),
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go deletionWorker.Run(sigCtx)
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("👤 Account Platform 启动于 %s", httpServer.Addr)
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		return err
	case <-sigCtx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

// productPurgers 按环境变量组装各业务产品的清除客户端。
//
// 少配一个产品就意味着那个产品的数据在删号后永远留着，所以这里把「没配」
// 大声喊出来：能起服（本地开发根本没有业务进程），但日志上不给它藏身之处。
// 配了地址却没有共享密钥则直接拒绝启动——那是配置写了一半的状态，
// 悄悄降级成不清除比起不来更危险。
func productPurgers(internalToken string) ([]account.ProductPurger, error) {
	targets := []struct{ product, baseURL string }{
		{product: "menu", baseURL: os.Getenv("MENU_BASE_URL")},
		{product: "english", baseURL: os.Getenv("ENGLISH_BASE_URL")},
	}
	var purgers []account.ProductPurger
	for _, target := range targets {
		if strings.TrimSpace(target.baseURL) == "" {
			log.Printf("⚠️  未配置 %s_BASE_URL，删号不会清除 %s 的业务数据",
				strings.ToUpper(target.product), target.product)
			continue
		}
		client, err := platformpurge.NewClient(target.product, target.baseURL, internalToken, nil)
		if err != nil {
			return nil, fmt.Errorf("配置 %s 数据清除失败: %w", target.product, err)
		}
		purgers = append(purgers, client)
		log.Printf("🗑️  删号将联动清除 %s 的业务数据", target.product)
	}
	return purgers, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/v1/auth/apple/challenges", s.handleAppleChallenge)
	mux.HandleFunc("/v1/auth/apple", s.handleAppleLogin)
	mux.HandleFunc("/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("/v1/me", s.handleMe)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
	return observability.HTTPMiddleware(handler)
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok")
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.schema.Ready(ctx); err != nil {
		http.Error(w, "database schema unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ready")
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

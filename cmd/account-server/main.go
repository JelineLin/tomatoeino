// account-server 是 tomato-platform 的统一身份与客户平台入口。
//
// 第一阶段提供 Sign in with Apple、平台 Token 生命周期和 PostgreSQL 会话；
// 账号删除与各业务数据迁移会在后续阶段沿这个边界继续实现。
package main

import (
	"context"
	"errors"
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
	"tomato-platform/internal/platformdb"
)

type databasePinger interface {
	Ping(context.Context) error
}

type server struct {
	db      databasePinger
	account *account.Service
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// 与现有业务进程保持一致：本地开发可从仓库根目录的 .env 读取，
	// 已由部署环境导出的变量不会被覆盖。
	_ = godotenv.Load()

	tokens, err := account.NewTokenManager(os.Getenv("ACCOUNT_TOKEN_SECRET"))
	if err != nil {
		return err
	}
	appleVerifier, err := account.NewAppleVerifier(strings.Split(os.Getenv("APPLE_CLIENT_IDS"), ","))
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := platformdb.Open(ctx, os.Getenv("PLATFORM_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()

	accountService := account.NewService(account.NewPostgresStore(pool), appleVerifier, tokens)
	srv := &server{db: pool, account: accountService}
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

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/v1/auth/apple/challenges", s.handleAppleChallenge)
	mux.HandleFunc("/v1/auth/apple", s.handleAppleLogin)
	mux.HandleFunc("/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("/v1/me", s.handleMe)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
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
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ready")
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

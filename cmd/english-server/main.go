// english-server 是 tomato-platform 的 English Coach 独立进程。
// 它复用 internal/llm 和同一套 Ark 凭证，但有自己的模型、提示词、SQLite、录音和前端。
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
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tomato-platform/internal/english"
)

type server struct {
	store     *english.Store
	generator english.LessonGenerator
	coach     english.Coach
	speech    english.SpeechRecognizer
	audioDir  string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "generate-today" {
		if err := runGenerateToday(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runServer(); err != nil {
		log.Fatal(err)
	}
}

func runServer() error {
	ctx := context.Background()
	store, err := english.OpenStore(envOr("ENGLISH_DB_PATH", "data/english/learning.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	planner, err := english.NewLLMPlanner(ctx)
	if err != nil {
		return fmt.Errorf("创建 English Coach 模型失败: %w", err)
	}
	englishToken := os.Getenv("ENGLISH_API_TOKEN")
	if englishToken == "" {
		englishToken = os.Getenv("API_TOKEN")
	}
	users, err := loadUsers(envOr("ENGLISH_USERS_PATH", envOr("USERS_PATH", "data/users.json")), englishToken)
	if err != nil {
		return fmt.Errorf("加载用户失败: %w", err)
	}
	s := &server{store: store, generator: planner, coach: planner, speech: english.NewSpeechRecognizerFromEnv(), audioDir: envOr("ENGLISH_AUDIO_DIR", "data/english/audio")}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("/api/english/today", s.handleToday)
	mux.HandleFunc("/api/english/lessons", s.handleLessons)
	mux.HandleFunc("/api/english/lessons/", s.handleLesson)
	mux.HandleFunc("/api/english/answers", s.handleAnswers)
	mux.HandleFunc("/api/english/attempts", s.handleAttempts)
	mux.HandleFunc("/api/english/attempts/", s.handleAttempt)
	mux.HandleFunc("/api/english/progress", s.handleProgress)
	mux.HandleFunc("/api/english/weekly-reports", s.handleReports)
	mux.HandleFunc("/api/english/profile", s.handleProfile)
	mux.HandleFunc("/api/english/generate-today", s.handleGenerateToday)
	mux.Handle("/", spaHandler(envOr("ENGLISH_WEB_DIR", filepath.Join("english-web", "out"))))
	httpServer := &http.Server{Addr: ":" + envOr("ENGLISH_PORT", "8450"), Handler: withCORS(withAuth(users, mux)), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 12 * time.Minute, WriteTimeout: 12 * time.Minute, IdleTimeout: 90 * time.Second}
	sigctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Printf("📘 English Coach 启动于 %s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP 服务退出: %v", err)
			stop()
		}
	}()
	<-sigctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

func envOr(k, v string) string {
	if x := strings.TrimSpace(os.Getenv(k)); x != "" {
		return x
	}
	return v
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func spaHandler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 人为加根再做 URL clean，../../etc/passwd 会被压回 etc/passwd，无法逃出静态目录。
		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean == "" || clean == "." {
			clean = "index.html"
		}
		candidate := filepath.Join(dir, clean)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			candidate = filepath.Join(candidate, "index.html")
		}
		if _, err := os.Stat(candidate); err != nil {
			candidate = filepath.Join(dir, "index.html")
		}
		http.ServeFile(w, r, candidate)
	})
}

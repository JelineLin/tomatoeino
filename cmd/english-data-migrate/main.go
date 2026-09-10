// english-data-migrate performs the explicit, one-time SQLite-to-PostgreSQL cutover.
// It never runs from english-server startup and never deletes the source database.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"tomato-platform/internal/english"
)

func main() {
	_ = godotenv.Load()
	var sourcePath, sourceUser, userID string
	flag.StringVar(&sourcePath, "source-db", envOr("ENGLISH_DB_PATH", "data/english/learning.db"), "旧 English SQLite 文件")
	flag.StringVar(&sourceUser, "source-user", "home", "SQLite 中的旧 user ID")
	flag.StringVar(&userID, "user-id", "", "目标平台用户 UUID（必填）")
	flag.Parse()

	if strings.TrimSpace(userID) == "" {
		log.Fatal("必须提供 --user-id")
	}
	if info, err := os.Stat(sourcePath); err != nil {
		log.Fatalf("读取源 SQLite 失败: %v", err)
	} else if info.IsDir() {
		log.Fatal("--source-db 必须指向 SQLite 文件")
	}
	source, err := english.OpenStore(sourcePath)
	if err != nil {
		log.Fatal(err)
	}
	defer source.Close()

	databaseURL := strings.TrimSpace(os.Getenv("ENGLISH_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	target, err := english.OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer target.Close()

	result, err := target.ImportSQLiteUser(ctx, source, strings.TrimSpace(sourceUser), strings.TrimSpace(userID))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("已把 SQLite 用户 %s 的 %d 条 English 数据迁移到平台用户 %s；课程 %d、阅读 %d、口语 %d、单词结果 %d。源数据库未删除，请核对后自行归档。\n",
		sourceUser, result.Total(), userID, result.Lessons, result.ReadingAttempts, result.SpeakingAttempts, result.WordResults)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

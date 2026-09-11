// menu-data-migrate performs the explicit, one-time JSON-to-PostgreSQL cutover.
// It is intentionally a separate operator command; cmd/server never imports or
// rewrites old data automatically during startup.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"tomato-platform/internal/menu"
	"tomato-platform/internal/observability"
	"tomato-platform/internal/platformdb"
)

func main() {
	_ = godotenv.Load()
	observability.Configure("menu-data-migrate")
	var userID, sourceUser, dataDir string
	flag.StringVar(&userID, "user-id", "", "目标平台用户 UUID（必填）")
	flag.StringVar(&sourceUser, "source-user", "", "源 JSON 目录名；默认与 user-id 相同，旧数据可填 home")
	flag.StringVar(&dataDir, "data-dir", "data", "Menu DATA_DIR")
	flag.Parse()

	userID = strings.TrimSpace(userID)
	if userID == "" {
		log.Fatal("必须提供 --user-id")
	}
	if sourceUser == "" {
		sourceUser = userID
	}
	sourceUser = strings.TrimSpace(sourceUser)
	if !safePathComponent(sourceUser) {
		log.Fatal("--source-user 必须是单个安全目录名")
	}

	sourceDir := filepath.Join(dataDir, "users", sourceUser)
	history, err := menu.NewHistoryStore(filepath.Join(sourceDir, "history.json"))
	if err != nil {
		log.Fatal(err)
	}
	inventory, err := menu.NewInventoryStore(filepath.Join(sourceDir, "inventory.json"))
	if err != nil {
		log.Fatal(err)
	}
	profile, err := menu.NewProfileStore(filepath.Join(sourceDir, "profile.json"))
	if err != nil {
		log.Fatal(err)
	}

	databaseURL := strings.TrimSpace(os.Getenv("MENU_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := platformdb.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	repo := menu.NewPostgresRepository(pool)
	if err := repo.Ready(ctx); err != nil {
		log.Fatal(err)
	}
	if err := repo.ImportSnapshotIfEmpty(ctx, userID, history.Snapshot(), inventory.List(""), profile.Get()); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("已把 %s 的 Menu JSON 数据迁移到平台用户 %s；源文件未删除，请核对后自行归档。\n", sourceUser, userID)
}

func safePathComponent(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, `/\\`)
}

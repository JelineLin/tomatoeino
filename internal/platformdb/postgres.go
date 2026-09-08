// Package platformdb 收敛 tomato-platform 访问 PostgreSQL 的基础设施约定。
//
// 这里只负责建立连接和验证可用性，不自动执行 migration。生产建库、升级和回滚
// 必须作为显式发布步骤完成，避免某个业务进程启动时悄悄改变全平台账本结构。
package platformdb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const connectTimeout = 5 * time.Second

// Open 创建 PostgreSQL 连接池，并在返回前完成一次 Ping。
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, fmt.Errorf("PLATFORM_DATABASE_URL 未设置")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析 PostgreSQL 连接配置失败: %w", err)
	}
	config.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("创建 PostgreSQL 连接池失败: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	return pool, nil
}

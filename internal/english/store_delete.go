package english

import (
	"context"
	"fmt"
	"strings"
)

// userScopedTables 是所有按 user_id 存数据的表，顺序即删除顺序：
// 子表在前、父表在后（打开了 foreign_keys，反过来会被外键挡住）。
//
// 新增带 user_id 的表必须加进这条列表，否则账号删除会留下残渣——
// TestDeleteUserCoversEverySchemaTable 会盯着这件事，漏了就红。
var userScopedTables = []string{
	"word_results",      // → speaking_attempts
	"reading_attempts",  // → lessons
	"speaking_attempts", // → lessons
	"lessons",
	"learning_profiles",
	"weak_points",
	"weekly_reports",
	"plan_versions",
	"job_runs",
}

// DeleteUser 清掉一个用户在 English Coach 的全部学习数据。
// 幂等：用户本来就没有数据也返回 nil——账号删除任务会重试，重试必须是安全的。
//
// 一个事务里删完：删到一半崩掉的话，剩下的记录会以「无主数据」的形态留在库里，
// 而账号那边已经准备硬删，没人再会来认领它们。
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("userID 不能为空")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始删除用户数据事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range userScopedTables {
		// 表名来自上面的常量列表，不来自任何外部输入；user_id 仍走参数绑定。
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id = ?", userID); err != nil {
			return fmt.Errorf("清除 %s 中的用户数据失败: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交删除用户数据失败: %w", err)
	}
	return nil
}

// AudioPathsFor 列出该用户已落盘的录音路径。删除音频文件时用它兜底：
// 正常情况下录音都在 audioDir/<uid>/ 下，删目录即可；这个列表用来发现
// 历史遗留在别处的文件，避免「库里没了、文件还躺在磁盘上」。
func (s *Store) AudioPathsFor(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT audio_path FROM speaking_attempts WHERE user_id = ? AND audio_path <> ''`, userID)
	if err != nil {
		return nil, fmt.Errorf("查询用户录音路径失败: %w", err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("查询用户录音路径失败: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("查询用户录音路径失败: %w", err)
	}
	return paths, nil
}

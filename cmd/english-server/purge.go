// purge.go —— 账号删除的联动清除：account-server 在硬删除账户前调这里，
// 把这个用户在 English Coach 的学习数据和录音一起清掉。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// purgeUser 是产品侧的清除实现，必须幂等——删除任务会退避重试。
//
// 顺序是先删文件、后删库：库里的 audio_path 是找到那些落在旧 ENGLISH_AUDIO_DIR 下
// 的历史录音的唯一线索，先清库的话，删文件这一步万一失败，重试时线索就没了，
// 录音会永远留在磁盘上——而账号那边已经报告「删干净了」。
func (s *server) purgeUser(ctx context.Context, userID string) error {
	recordings, err := s.store.AudioPathsFor(ctx, userID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.audioDir, userID)); err != nil {
		return fmt.Errorf("删除用户 %s 的录音目录失败: %w", userID, err)
	}
	for _, path := range recordings {
		if !ownedRecording(path, userID) {
			// 库里的路径不该指向不含本人 ID 的地方；真出现了就留着不动，
			// 让人来看一眼，绝不按一个可疑路径去 rm。
			log.Printf("⚠️  跳过可疑录音路径 %q（用户 %s）", path, userID)
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除用户 %s 的录音 %s 失败: %w", userID, path, err)
		}
	}
	if err := s.store.DeleteUser(ctx, userID); err != nil {
		return err
	}
	log.Printf("🗑️  已清除用户 %s 的全部英语学习数据（%d 个录音）", userID, len(recordings))
	return nil
}

// ownedRecording 判断这条录音路径是否确实属于该用户：必须把 userID 完整地
// 作为一段路径包含进来，且不含 .. ——录音路径由 saveAudio 生成，本就长这样。
func ownedRecording(path, userID string) bool {
	cleaned := filepath.Clean(path)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return false
	}
	for _, segment := range strings.Split(cleaned, string(filepath.Separator)) {
		if segment == ".." {
			return false
		}
		if segment == userID {
			return true
		}
	}
	return false
}

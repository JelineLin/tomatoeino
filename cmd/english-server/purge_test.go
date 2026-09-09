package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tomato-platform/internal/english"
)

// 删号必须同时带走学习记录和录音文件——录音留在磁盘上等于「说删了其实没删」。
func TestPurgeUserRemovesRowsAndRecordings(t *testing.T) {
	ctx := context.Background()
	store, err := english.OpenStore(filepath.Join(t.TempDir(), "learning.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const userID = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"
	audioDir := t.TempDir()
	recording := filepath.Join(audioDir, userID, "2026", "09", "09", "attempt.ogg")
	if err := os.MkdirAll(filepath.Dir(recording), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recording, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	lesson, _, err := store.PutLesson(ctx, english.Lesson{
		UserID: userID, Date: "2026-09-09", Title: "T", Passage: "Text",
		Questions: []english.Question{{ID: "q1", Answer: "A", Explain: "because"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSpeakingAttempt(ctx, english.SpeakingAttempt{
		UserID: userID, LessonID: lesson.ID, AudioPath: recording, DurationSec: 3, Transcript: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	s := &server{store: store, audioDir: audioDir}
	if err := s.purgeUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(audioDir, userID)); !os.IsNotExist(err) {
		t.Errorf("录音目录应被删除: %v", err)
	}
	if _, err := store.Lesson(ctx, userID, lesson.ID); err == nil {
		t.Error("课程记录应被删除")
	}

	// 幂等：删除任务会重试。
	if err := s.purgeUser(ctx, userID); err != nil {
		t.Errorf("重复清除应幂等: %v", err)
	}
}

// 库里的录音路径万一被写坏，绝不能按它去 rm 一个不属于这个用户的文件。
func TestOwnedRecordingRejectsForeignPaths(t *testing.T) {
	const userID = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"
	for path, want := range map[string]bool{
		"data/english/audio/" + userID + "/2026/09/09/a.ogg": true,
		"/var/lib/english/" + userID + "/a.ogg":              true,
		"data/english/audio/other-user/a.ogg":                false,
		"../../etc/passwd":                                   false,
		"data/english/audio/" + userID + "/../../../etc/x":   false,
		"":                                                   false,
	} {
		if got := ownedRecording(path, userID); got != want {
			t.Errorf("ownedRecording(%q) = %v, want %v", path, got, want)
		}
	}
}

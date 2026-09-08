package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"tomato-platform/internal/english"
)

func runGenerateToday(ctx context.Context) error {
	now := time.Now()
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return nil
	}
	store, err := english.OpenStore(envOr("ENGLISH_DB_PATH", "data/english/learning.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	planner, err := english.NewLLMPlanner(ctx)
	if err != nil {
		return err
	}
	ids := strings.Split(envOr("ENGLISH_USER_IDS", defaultUserID), ",")
	var failures []string
	for _, raw := range ids {
		uid := strings.TrimSpace(raw)
		if uid == "" {
			continue
		}
		date := now.Format("2006-01-02")
		started, err := store.StartJob(ctx, uid, date, "generate-today")
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if !started {
			continue
		}
		lesson, _, err := english.GenerateToday(ctx, store, planner, uid, now)
		if err != nil {
			_ = store.FinishJob(ctx, uid, date, "generate-today", "failed", "skipped", err.Error())
			failures = append(failures, uid+": "+err.Error())
			continue
		}
		notifyStatus := "skipped"
		var notifyErr error
		if notificationConfigured() {
			claimed, claimErr := store.ClaimNotification(ctx, uid, date, "generate-today")
			if claimErr != nil {
				notifyErr = claimErr
				notifyStatus = "failed"
			} else if claimed {
				notifyErr = notify(ctx, uid, lesson)
				notifyStatus = "succeeded"
				if notifyErr != nil {
					notifyStatus = "failed"
				}
			} else {
				notifyStatus = "" // 保留上一次的 sending/succeeded/failed，绝不再次调用。
			}
		}
		message := ""
		if notifyErr != nil {
			message = notifyErr.Error()
		}
		_ = store.FinishJob(ctx, uid, date, "generate-today", "succeeded", notifyStatus, message)
		if now.Weekday() == time.Friday {
			if err := saveFridayReport(ctx, store, uid, now); err != nil {
				failures = append(failures, uid+" 周报: "+err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("部分用户生成失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

func saveFridayReport(ctx context.Context, store *english.Store, userID string, now time.Time) error {
	p, err := store.Progress(ctx, userID, now)
	if err != nil {
		return err
	}
	monday := now.AddDate(0, 0, -int((int(now.Weekday())+6)%7)).Format("2006-01-02")
	focus := "保持稳定完成课程，并复习本周问题词"
	if p.ReadingAccuracy4W > 0 && p.ReadingAccuracy4W < .6 {
		focus = "降低生词密度，逐段精读并复述主旨"
	} else if p.SpeakingAccuracy4W > 0 && p.SpeakingAccuracy4W < .7 {
		focus = "放慢语速，优先练清词尾辅音和问题词"
	}
	r := english.WeeklyReport{UserID: userID, WeekStart: monday, CompletionRate: p.CompletionRate4W, ReadingAccuracy: p.ReadingAccuracy4W, SpeakingWPM: p.SpeakingSpeedWPM, ProblemWords: p.RecurringErrors, Summary: fmt.Sprintf("最近四周完成 %d/%d 课，阅读正确率 %.0f%%，平均朗读语速 %.0f WPM。", p.LessonsCompleted, p.LessonsAssigned, p.ReadingAccuracy4W*100, p.SpeakingSpeedWPM), NextFocus: focus}
	if _, err := store.SaveWeeklyReport(ctx, r); err != nil {
		return err
	}
	return store.ApplyWeeklyDifficulty(ctx, userID, monday, p.RecommendedDifficulty, "周五按完成率、阅读与朗读表现最多调整一级")
}

// 通知是课程落库后的旁路动作：通知失败只记 job_runs，不回滚已经生成的课程。
// ENGLISH_NOTIFY_URL 可接 hermas/微信网关；不配置时明确跳过。
func notify(ctx context.Context, userID string, lesson english.Lesson) error {
	url := strings.TrimSpace(os.Getenv("ENGLISH_NOTIFY_URL"))
	if url == "" {
		return nil
	}
	base := envOr("ENGLISH_PUBLIC_URL", "https://english.jelinelin.com")
	body, _ := json.Marshal(map[string]string{"user_id": userID, "title": "今日英语课程已生成：" + lesson.Title, "url": base})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("ENGLISH_NOTIFY_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("通知网关返回 %d", resp.StatusCode)
	}
	return nil
}

func notificationConfigured() bool {
	return strings.TrimSpace(os.Getenv("ENGLISH_NOTIFY_URL")) != ""
}

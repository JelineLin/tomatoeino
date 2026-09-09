package account

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

var ErrNoDeletionJob = errors.New("没有待处理的账号删除任务")

type AppleCredential struct {
	Subject    string
	ClientID   string
	Ciphertext []byte
	Nonce      []byte
}

type DeletionJob struct {
	ID           string            `json:"id"`
	UserID       string            `json:"-"`
	Status       string            `json:"status"`
	AttemptCount int               `json:"attempt_count"`
	RequestedAt  time.Time         `json:"requested_at"`
	Credentials  []AppleCredential `json:"-"`
}

type DeletionStore interface {
	ClaimDeletionJob(context.Context, time.Time) (DeletionJob, error)
	CompleteDeletion(context.Context, DeletionJob, string) error
	RetryDeletion(context.Context, DeletionJob, time.Time, string) error
}

type DeletionWorker struct {
	store       DeletionStore
	appleTokens AppleTokenService
	cipher      *TokenCipher
	now         func() time.Time
}

func NewDeletionWorker(store DeletionStore, appleTokens AppleTokenService, cipher *TokenCipher) *DeletionWorker {
	return &DeletionWorker{store: store, appleTokens: appleTokens, cipher: cipher, now: time.Now}
}

func (w *DeletionWorker) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		handled, err := w.ProcessOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("account deletion worker: %v", err)
		}
		if handled {
			timer.Reset(100 * time.Millisecond)
		} else {
			timer.Reset(5 * time.Second)
		}
	}
}

func (w *DeletionWorker) ProcessOne(ctx context.Context) (bool, error) {
	now := w.now().UTC()
	job, err := w.store.ClaimDeletionJob(ctx, now.Add(5*time.Minute))
	if errors.Is(err, ErrNoDeletionJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	notes := make([]string, 0, len(job.Credentials))
	for _, credential := range job.Credentials {
		refreshToken, err := w.cipher.OpenFor(
			appleCredentialAAD(credential.Subject, credential.ClientID),
			credential.Ciphertext, credential.Nonce,
		)
		if err == nil {
			err = w.appleTokens.Revoke(ctx, credential.ClientID, refreshToken)
		}
		if errors.Is(err, ErrInvalidAppleAuthorization) {
			// Apple 凭证已失效时仍需履行本地删除；Apple 官方要求此时引导用户手动撤销。
			notes = append(notes, "apple credential unavailable for automatic revocation")
			continue
		}
		if err != nil {
			retryAt := now.Add(deletionRetryDelay(job.AttemptCount))
			message := truncateError(err, 1000)
			if retryErr := w.store.RetryDeletion(ctx, job, retryAt, message); retryErr != nil {
				return true, fmt.Errorf("删除任务失败且保存重试状态失败: %v; 原因: %w", retryErr, err)
			}
			return true, fmt.Errorf("删除任务将在 %s 重试: %w", retryAt.Format(time.RFC3339), err)
		}
	}
	if err := w.store.CompleteDeletion(ctx, job, strings.Join(notes, "; ")); err != nil {
		return true, err
	}
	return true, nil
}

func appleCredentialAAD(subject, clientID string) string {
	return subject + "\x00" + clientID
}

func deletionRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	delay := time.Minute * time.Duration(1<<(attempt-1))
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

func truncateError(err error, maxRunes int) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	runes := []rune(message)
	if len(runes) <= maxRunes {
		return message
	}
	return string(runes[:maxRunes])
}

package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tomato-platform/internal/observability"
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
	RequestID    string            `json:"-"`
	Credentials  []AppleCredential `json:"-"`
}

// ProductPurger 是一个业务产品的数据清除入口（由 internal/platformpurge 实现）。
// 账号是平台的，业务数据却在各产品自己的进程里，硬删除账户前必须逐个清干净——
// 清不掉就整单重试，绝不允许「账户没了、业务数据成孤儿」。
type ProductPurger interface {
	Product() string
	Purge(context.Context, string) error
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
	purgers     []ProductPurger
	now         func() time.Time
}

// NewDeletionWorker 组装删除工作者。purgers 是本部署接入的业务产品；一个都不配
// 时删除仍会完成（本地开发没有业务进程），但会在任务备注里留下痕迹，
// 免得「以为删干净了」这件事无声无息地发生。
func NewDeletionWorker(store DeletionStore, appleTokens AppleTokenService, cipher *TokenCipher, purgers ...ProductPurger) *DeletionWorker {
	return &DeletionWorker{store: store, appleTokens: appleTokens, cipher: cipher, purgers: purgers, now: time.Now}
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
			slog.ErrorContext(ctx, "account deletion worker failed", "error", err)
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
	ctx = observability.WithRequestID(ctx, job.RequestID)
	observability.SetUserID(ctx, job.UserID)
	slog.InfoContext(ctx, "account deletion started", "job_id", job.ID, "attempt", job.AttemptCount)

	notes := make([]string, 0, len(job.Credentials)+1)
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
			return w.scheduleRetry(ctx, job, now, err)
		}
	}

	// 业务数据必须先于账户数据清掉：account.users 一旦硬删，user_id 就只剩删除任务里
	// 那份快照，任何一处漏清都再也没人认领。清不掉就整单退避重试——
	// 各产品的清除接口是幂等的，重试会把已经清过的那几个安全地再走一遍。
	if len(w.purgers) == 0 {
		notes = append(notes, "no product purge target configured")
	}
	for _, purger := range w.purgers {
		if err := purger.Purge(ctx, job.UserID); err != nil {
			return w.scheduleRetry(ctx, job, now, err)
		}
		slog.InfoContext(ctx, "product data purged", "job_id", job.ID, "product", purger.Product())
	}

	if err := w.store.CompleteDeletion(ctx, job, strings.Join(notes, "; ")); err != nil {
		return true, err
	}
	slog.InfoContext(ctx, "account deletion completed", "job_id", job.ID)
	return true, nil
}

// scheduleRetry 把这次失败写回任务并按退避排下一次。任务留在库里、账户仍是
// deleting（会话已撤销），所以「删了一半」对用户始终表现为账号已不可用。
func (w *DeletionWorker) scheduleRetry(ctx context.Context, job DeletionJob, now time.Time, cause error) (bool, error) {
	retryAt := now.Add(deletionRetryDelay(job.AttemptCount))
	message := truncateError(cause, 1000)
	if retryErr := w.store.RetryDeletion(ctx, job, retryAt, message); retryErr != nil {
		return true, fmt.Errorf("删除任务失败且保存重试状态失败: %v; 原因: %w", retryErr, cause)
	}
	slog.WarnContext(ctx, "account deletion scheduled for retry", "job_id", job.ID, "retry_at", retryAt, "error", cause)
	return true, fmt.Errorf("删除任务将在 %s 重试: %w", retryAt.Format(time.RFC3339), cause)
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

package account

import (
	"context"
	"crypto/sha256"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tomato-platform/internal/observability"
)

func TestPostgresDeletionRetainsRequestCorrelation(t *testing.T) {
	databaseURL := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL 未配置")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPostgresStore(pool)
	if err := store.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	const userID = "33333333-3333-4333-8333-333333333333"
	const sessionID = "44444444-4444-4444-8444-444444444444"
	_, _ = pool.Exec(ctx, `DELETE FROM audit.security_events WHERE target_id IN ($1,$2)`, userID, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM account.account_deletion_jobs WHERE user_id=$1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM account.users WHERE id=$1`, userID)
	defer pool.Exec(ctx, `DELETE FROM account.account_deletion_jobs WHERE user_id=$1`, userID)
	defer pool.Exec(ctx, `DELETE FROM audit.security_events WHERE request_id='delete-request-123'`)
	defer pool.Exec(ctx, `DELETE FROM account.users WHERE id=$1`, userID)

	if _, err := pool.Exec(ctx, `INSERT INTO account.users(id,status) VALUES($1,'active')`, userID); err != nil {
		t.Fatal(err)
	}
	refreshHash := sha256.Sum256([]byte("observability-integration-refresh"))
	if _, err := pool.Exec(ctx, `INSERT INTO account.sessions(id,user_id,refresh_token_hash,device_id,expires_at)
 VALUES($1,$2,$3,'integration-test',$4)`, sessionID, userID, refreshHash[:], time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	requestContext := observability.WithRequestID(ctx, "delete-request-123")
	job, err := store.RequestDeletion(requestContext, userID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if job.RequestID != "delete-request-123" {
		t.Fatalf("deletion job request ID = %q", job.RequestID)
	}
	var auditRequestID string
	if err := pool.QueryRow(ctx, `SELECT request_id FROM audit.security_events
 WHERE event_type='account.deletion.requested' AND target_id=$1`, userID).Scan(&auditRequestID); err != nil || auditRequestID != job.RequestID {
		t.Fatalf("request audit ID = %q, %v", auditRequestID, err)
	}

	claimed, err := store.ClaimDeletionJob(ctx, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != job.ID || claimed.RequestID != job.RequestID {
		t.Fatalf("claimed job lost correlation: %+v", claimed)
	}
	if err := store.CompleteDeletion(requestContext, claimed, "integration test"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT request_id FROM audit.security_events
 WHERE event_type='account.deletion.completed' AND target_id=$1`, job.ID).Scan(&auditRequestID); err != nil || auditRequestID != job.RequestID {
		t.Fatalf("completion audit ID = %q, %v", auditRequestID, err)
	}
}

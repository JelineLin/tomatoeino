package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tomato-platform/internal/observability"
)

func (s *PostgresStore) RequestDeletion(ctx context.Context, userID, sessionID string) (DeletionJob, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DeletionJob{}, fmt.Errorf("开始账号删除事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var validSession bool
	err = tx.QueryRow(ctx, `
		SELECT user_account.status,
		       EXISTS (
		           SELECT 1 FROM account.sessions session
		           WHERE session.id = $2 AND session.user_id = user_account.id
		             AND session.expires_at > now()
		       )
		FROM account.users user_account
		WHERE user_account.id = $1
		FOR UPDATE`, userID, sessionID).Scan(&status, &validSession)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeletionJob{}, ErrInvalidSession
	}
	if err != nil {
		return DeletionJob{}, fmt.Errorf("查询待删除账号失败: %w", err)
	}
	if !validSession {
		return DeletionJob{}, ErrInvalidSession
	}
	if status == "deleting" {
		var existing DeletionJob
		err = tx.QueryRow(ctx, `
			SELECT id::text, user_id::text, status, attempt_count, requested_at, request_id
			FROM account.account_deletion_jobs
			WHERE user_id = $1 AND status IN ('pending', 'running', 'failed')
			ORDER BY requested_at DESC LIMIT 1`, userID).
			Scan(&existing.ID, &existing.UserID, &existing.Status, &existing.AttemptCount, &existing.RequestedAt, &existing.RequestID)
		if errors.Is(err, pgx.ErrNoRows) {
			return DeletionJob{}, ErrAccountUnavailable
		}
		if err != nil {
			return DeletionJob{}, fmt.Errorf("查询已有账号删除任务失败: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return DeletionJob{}, fmt.Errorf("提交删除任务查询失败: %w", err)
		}
		return existing, nil
	}
	if status != "active" {
		return DeletionJob{}, ErrAccountUnavailable
	}
	var activeSession bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM account.sessions
			WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL AND expires_at > now()
		)`, sessionID, userID).Scan(&activeSession)
	if err != nil {
		return DeletionJob{}, fmt.Errorf("校验删除申请会话失败: %w", err)
	}
	if !activeSession {
		return DeletionJob{}, ErrInvalidSession
	}

	result, err := tx.Exec(ctx, `
		UPDATE account.users
		SET status = 'deleting', updated_at = now()
		WHERE id = $1 AND status = 'active'`, userID)
	if err != nil {
		return DeletionJob{}, fmt.Errorf("冻结待删除账号失败: %w", err)
	}
	if result.RowsAffected() != 1 {
		return DeletionJob{}, ErrAccountUnavailable
	}
	if _, err = tx.Exec(ctx, `
		UPDATE account.sessions
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE user_id = $1`, userID); err != nil {
		return DeletionJob{}, fmt.Errorf("撤销待删除账号会话失败: %w", err)
	}

	var job DeletionJob
	err = tx.QueryRow(ctx, `
		INSERT INTO account.account_deletion_jobs(user_id, request_id)
		VALUES ($1, $2)
		RETURNING id::text, user_id::text, status, attempt_count, requested_at, request_id`, userID, observability.RequestID(ctx)).
		Scan(&job.ID, &job.UserID, &job.Status, &job.AttemptCount, &job.RequestedAt, &job.RequestID)
	if err != nil {
		return DeletionJob{}, fmt.Errorf("创建账号删除任务失败: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit.security_events(actor_user_id, event_type, target_type, target_id, request_id)
		VALUES ($1, 'account.deletion.requested', 'user', $1::uuid::text, $2)`, userID, observability.RequestID(ctx)); err != nil {
		return DeletionJob{}, fmt.Errorf("记录账号删除申请审计失败: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return DeletionJob{}, fmt.Errorf("提交账号删除申请失败: %w", err)
	}
	return job, nil
}

func (s *PostgresStore) ClaimDeletionJob(ctx context.Context, lockedUntil time.Time) (DeletionJob, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DeletionJob{}, fmt.Errorf("开始领取删除任务事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var job DeletionJob
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM account.account_deletion_jobs
			WHERE (status IN ('pending', 'failed') AND next_attempt_at <= now())
			   OR (status = 'running' AND locked_until < now())
			ORDER BY next_attempt_at, requested_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE account.account_deletion_jobs AS job
		SET status = 'running',
		    attempt_count = attempt_count + 1,
		    started_at = COALESCE(started_at, now()),
		    locked_until = $1
		FROM candidate
		WHERE job.id = candidate.id
		RETURNING job.id::text, job.user_id::text, job.status,
		          job.attempt_count, job.requested_at, job.request_id`, lockedUntil).
		Scan(&job.ID, &job.UserID, &job.Status, &job.AttemptCount, &job.RequestedAt, &job.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeletionJob{}, ErrNoDeletionJob
	}
	if err != nil {
		return DeletionJob{}, fmt.Errorf("领取账号删除任务失败: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT identity.provider_subject,
		       credential.client_id,
		       credential.refresh_token_ciphertext,
		       credential.refresh_token_nonce
		FROM account.apple_credentials credential
		JOIN account.identities identity ON identity.id = credential.identity_id
		WHERE identity.user_id = $1`, job.UserID)
	if err != nil {
		return DeletionJob{}, fmt.Errorf("读取待撤销 Apple 凭证失败: %w", err)
	}
	for rows.Next() {
		var credential AppleCredential
		if err := rows.Scan(&credential.Subject, &credential.ClientID, &credential.Ciphertext, &credential.Nonce); err != nil {
			rows.Close()
			return DeletionJob{}, fmt.Errorf("读取待撤销 Apple 凭证失败: %w", err)
		}
		job.Credentials = append(job.Credentials, credential)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DeletionJob{}, fmt.Errorf("读取待撤销 Apple 凭证失败: %w", err)
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return DeletionJob{}, fmt.Errorf("提交领取删除任务事务失败: %w", err)
	}
	return job, nil
}

func (s *PostgresStore) CompleteDeletion(ctx context.Context, job DeletionJob, note string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开始完成账号删除事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, `
		DELETE FROM audit.security_events
		WHERE actor_user_id = $1
		   OR (target_type = 'user' AND target_id = $1::text)`, job.UserID); err != nil {
		return fmt.Errorf("清理账号审计标识失败: %w", err)
	}

	// 仅有该用户一名成员的家庭随账号删除；仍有其他成员的共享家庭保留。
	if _, err = tx.Exec(ctx, `
		DELETE FROM account.households household
		WHERE EXISTS (
			SELECT 1 FROM account.household_members member
			WHERE member.household_id = household.id AND member.user_id = $1
		) AND NOT EXISTS (
			SELECT 1 FROM account.household_members member
			WHERE member.household_id = household.id AND member.user_id <> $1
		)`, job.UserID); err != nil {
		return fmt.Errorf("删除用户独占家庭失败: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM account.users WHERE id = $1`, job.UserID); err != nil {
		return fmt.Errorf("删除账号数据失败: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE account.account_deletion_jobs
		SET status = 'completed', completed_at = now(), locked_until = NULL,
		    error_message = $3
		WHERE id = $1 AND user_id = $2 AND status = 'running'`, job.ID, job.UserID, note)
	if err != nil {
		return fmt.Errorf("完成账号删除任务失败: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("账号删除任务状态已变化")
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit.security_events(event_type, target_type, target_id, request_id, details)
		VALUES ('account.deletion.completed', 'deletion_job', $1, $2, jsonb_build_object('note', $3::text))`,
		job.ID, job.RequestID, note); err != nil {
		return fmt.Errorf("记录账号删除完成审计失败: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交账号删除事务失败: %w", err)
	}
	return nil
}

func (s *PostgresStore) RetryDeletion(ctx context.Context, job DeletionJob, retryAt time.Time, message string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE account.account_deletion_jobs
		SET status = 'failed', next_attempt_at = $2, locked_until = NULL,
		    error_message = $3
		WHERE id = $1 AND status = 'running'`, job.ID, retryAt, message)
	if err != nil {
		return fmt.Errorf("保存账号删除重试状态失败: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("账号删除任务状态已变化")
	}
	return nil
}

package account

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tomato-platform/internal/observability"
)

var (
	ErrInvalidChallenge   = errors.New("登录挑战无效或已使用")
	ErrInvalidSession     = errors.New("会话无效")
	ErrAccountUnavailable = errors.New("账号当前不可用")
	ErrProductUnavailable = errors.New("产品权限当前不可用")
)

type User struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Products    []string `json:"products"`
}

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type AppleLoginParams struct {
	ChallengeID            string
	ProductCode            string
	Identity               AppleIdentity
	AppleClientID          string
	AppleRefreshHash       [sha256.Size]byte
	AppleRefreshCiphertext []byte
	AppleRefreshNonce      []byte
	DisplayName            string
	Device                 Device
	RefreshHash            [sha256.Size]byte
	RefreshExpiresAt       time.Time
}

type UserSession struct {
	User      User
	SessionID string
}

type AccountStore interface {
	CreateChallenge(context.Context, string, [sha256.Size]byte, time.Time) (string, error)
	LoginApple(context.Context, AppleLoginParams) (UserSession, error)
	RotateSession(context.Context, [sha256.Size]byte, [sha256.Size]byte, time.Time) (UserSession, error)
	UserForSession(context.Context, string, string) (User, error)
	RevokeSession(context.Context, string, string) error
	RequestDeletion(context.Context, string, string) (DeletionJob, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Ready(ctx context.Context) error {
	var ready bool
	err := s.pool.QueryRow(ctx, `
		SELECT to_regclass('account.users') IS NOT NULL
		   AND to_regclass('account.apple_credentials') IS NOT NULL
		   AND to_regclass('account.account_deletion_jobs') IS NOT NULL
		   AND EXISTS (
		       SELECT 1 FROM information_schema.columns
		       WHERE table_schema = 'account' AND table_name = 'account_deletion_jobs'
		         AND column_name = 'request_id'
		   )`).Scan(&ready)
	if err != nil {
		return fmt.Errorf("检查账户数据库结构失败: %w", err)
	}
	if !ready {
		return fmt.Errorf("账户数据库 migration 尚未完成")
	}
	return nil
}

func (s *PostgresStore) CreateChallenge(ctx context.Context, productCode string, nonceHash [sha256.Size]byte, expiresAt time.Time) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO account.login_challenges(product_code, nonce_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id::text`, productCode, nonceHash[:], expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("创建 Apple 登录挑战失败: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) LoginApple(ctx context.Context, p AppleLoginParams) (UserSession, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return UserSession{}, fmt.Errorf("开始登录事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	nonceHash := HashOpaqueToken(p.Identity.Nonce)
	result, err := tx.Exec(ctx, `
		UPDATE account.login_challenges
		SET used_at = now()
		WHERE id = $1 AND product_code = $2 AND nonce_hash = $3
		  AND used_at IS NULL AND expires_at > now()`,
		p.ChallengeID, p.ProductCode, nonceHash[:])
	if err != nil {
		return UserSession{}, fmt.Errorf("消费 Apple 登录挑战失败: %w", err)
	}
	if result.RowsAffected() != 1 {
		return UserSession{}, ErrInvalidChallenge
	}

	// 同一 Apple subject 的并发首次登录必须串行，否则可能各建一个 user 后争抢唯一身份。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, p.Identity.Subject); err != nil {
		return UserSession{}, fmt.Errorf("锁定 Apple 身份失败: %w", err)
	}

	user := User{}
	var status string
	var identityID string
	err = tx.QueryRow(ctx, `
		SELECT u.id::text, u.status, u.display_name, i.id::text
		FROM account.identities i
		JOIN account.users u ON u.id = i.user_id
		WHERE i.provider = 'apple' AND i.provider_subject = $1
		FOR UPDATE OF u`, p.Identity.Subject).Scan(&user.ID, &status, &user.DisplayName, &identityID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		err = tx.QueryRow(ctx, `
			INSERT INTO account.users(display_name) VALUES ($1)
			RETURNING id::text, status, display_name`, p.DisplayName).
			Scan(&user.ID, &status, &user.DisplayName)
		if err != nil {
			return UserSession{}, fmt.Errorf("创建平台用户失败: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO account.parties(user_id) VALUES ($1)`, user.ID); err != nil {
			return UserSession{}, fmt.Errorf("创建客户主体失败: %w", err)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO account.identities(
				user_id, provider, provider_subject, email, email_verified, last_login_at
			) VALUES ($1, 'apple', $2, NULLIF($3, ''), $4, now())
			RETURNING id::text`, user.ID, p.Identity.Subject, p.Identity.Email, p.Identity.EmailVerified).
			Scan(&identityID)
		if err != nil {
			return UserSession{}, fmt.Errorf("绑定 Apple 身份失败: %w", err)
		}
	case err != nil:
		return UserSession{}, fmt.Errorf("查询 Apple 身份失败: %w", err)
	case status != "active":
		return UserSession{}, ErrAccountUnavailable
	default:
		if _, err = tx.Exec(ctx, `
			UPDATE account.identities
			SET email = COALESCE(NULLIF($2, ''), email),
			    email_verified = CASE WHEN $2 = '' THEN email_verified ELSE $3 END,
			    last_login_at = now()
			WHERE provider = 'apple' AND provider_subject = $1`,
			p.Identity.Subject, p.Identity.Email, p.Identity.EmailVerified); err != nil {
			return UserSession{}, fmt.Errorf("更新 Apple 身份失败: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO account.apple_credentials(
			identity_id, client_id, refresh_token_hash,
			refresh_token_ciphertext, refresh_token_nonce
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (identity_id, client_id, refresh_token_hash) DO UPDATE SET
			refresh_token_ciphertext = EXCLUDED.refresh_token_ciphertext,
			refresh_token_nonce = EXCLUDED.refresh_token_nonce,
			updated_at = now()`, identityID, p.AppleClientID, p.AppleRefreshHash[:],
		p.AppleRefreshCiphertext, p.AppleRefreshNonce); err != nil {
		return UserSession{}, fmt.Errorf("保存 Apple refresh token 失败: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO account.product_memberships(user_id, product_code)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, user.ID, p.ProductCode); err != nil {
		return UserSession{}, fmt.Errorf("开通产品失败: %w", err)
	}
	var productStatus string
	if err = tx.QueryRow(ctx, `
		SELECT status FROM account.product_memberships
		WHERE user_id = $1 AND product_code = $2`, user.ID, p.ProductCode).Scan(&productStatus); err != nil {
		return UserSession{}, fmt.Errorf("查询产品权限失败: %w", err)
	}
	if productStatus != "active" {
		return UserSession{}, ErrProductUnavailable
	}

	var sessionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO account.sessions(
			user_id, refresh_token_hash, device_id, device_name, platform, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text`, user.ID, p.RefreshHash[:], p.Device.ID, p.Device.Name,
		p.Device.Platform, p.RefreshExpiresAt).Scan(&sessionID)
	if err != nil {
		return UserSession{}, fmt.Errorf("创建平台会话失败: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit.security_events(actor_user_id, event_type, target_type, target_id, request_id)
		VALUES ($1, 'auth.apple.login', 'session', $2, $3)`, user.ID, sessionID, observability.RequestID(ctx)); err != nil {
		return UserSession{}, fmt.Errorf("记录登录审计失败: %w", err)
	}
	user.Products, err = activeProducts(ctx, tx, user.ID)
	if err != nil {
		return UserSession{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return UserSession{}, fmt.Errorf("提交登录事务失败: %w", err)
	}
	return UserSession{User: user, SessionID: sessionID}, nil
}

func (s *PostgresStore) RotateSession(ctx context.Context, oldHash, newHash [sha256.Size]byte, expiresAt time.Time) (UserSession, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return UserSession{}, fmt.Errorf("开始 token 轮换事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out UserSession
	err = tx.QueryRow(ctx, `
		UPDATE account.sessions s
		SET refresh_token_hash = $2, expires_at = $3, last_seen_at = now()
		FROM account.users u
		WHERE s.refresh_token_hash = $1 AND s.user_id = u.id
		  AND s.revoked_at IS NULL AND s.expires_at > now() AND u.status = 'active'
		RETURNING u.id::text, u.display_name, s.id::text`,
		oldHash[:], newHash[:], expiresAt).
		Scan(&out.User.ID, &out.User.DisplayName, &out.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSession{}, ErrInvalidSession
	}
	if err != nil {
		return UserSession{}, fmt.Errorf("轮换 refresh token 失败: %w", err)
	}
	out.User.Products, err = activeProducts(ctx, tx, out.User.ID)
	if err != nil {
		return UserSession{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit.security_events(actor_user_id, event_type, target_type, target_id, request_id)
		VALUES ($1, 'auth.session.refresh', 'session', $2, $3)`, out.User.ID, out.SessionID, observability.RequestID(ctx)); err != nil {
		return UserSession{}, fmt.Errorf("记录 token 轮换审计失败: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return UserSession{}, fmt.Errorf("提交 token 轮换事务失败: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) UserForSession(ctx context.Context, userID, sessionID string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.display_name
		FROM account.sessions s
		JOIN account.users u ON u.id = s.user_id
		WHERE s.id = $1 AND u.id = $2 AND u.status = 'active'
		  AND s.revoked_at IS NULL AND s.expires_at > now()`, sessionID, userID).
		Scan(&user.ID, &user.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidSession
	}
	if err != nil {
		return User{}, fmt.Errorf("查询平台会话失败: %w", err)
	}
	user.Products, err = activeProducts(ctx, s.pool, user.ID)
	return user, err
}

func (s *PostgresStore) RevokeSession(ctx context.Context, userID, sessionID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开始登出事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE account.sessions SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("撤销平台会话失败: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrInvalidSession
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit.security_events(actor_user_id, event_type, target_type, target_id, request_id)
		VALUES ($1, 'auth.session.logout', 'session', $2, $3)`, userID, sessionID, observability.RequestID(ctx)); err != nil {
		return fmt.Errorf("记录登出审计失败: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交登出事务失败: %w", err)
	}
	return nil
}

type rowQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func activeProducts(ctx context.Context, db rowQuerier, userID string) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT product_code FROM account.product_memberships
		WHERE user_id = $1 AND status = 'active'
		ORDER BY product_code`, userID)
	if err != nil {
		return nil, fmt.Errorf("查询产品权限失败: %w", err)
	}
	defer rows.Close()
	products := make([]string, 0, 2)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("读取产品权限失败: %w", err)
		}
		products = append(products, code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取产品权限失败: %w", err)
	}
	return products, nil
}

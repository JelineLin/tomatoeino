package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	accessTokenIssuer   = "tomato-platform"
	accessTokenAudience = "tomato-platform-api"
	accessTokenTTL      = 15 * time.Minute
	refreshTokenTTL     = 30 * 24 * time.Hour
)

// AccessClaims 是平台内部短期访问令牌。权限仍以数据库中的产品状态为准，
// JWT 只承载稳定的用户和会话标识，避免权限变更后旧令牌继续携带过期权限。
type AccessClaims struct {
	SessionID string `json:"sid"`
	TokenType string `json:"typ"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret []byte
	now    func() time.Time
}

func NewTokenManager(secret string) (*TokenManager, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("ACCOUNT_TOKEN_SECRET 至少需要 32 个字符")
	}
	return &TokenManager{secret: []byte(secret), now: time.Now}, nil
}

func (m *TokenManager) IssueAccessToken(userID, sessionID string) (string, time.Time, error) {
	now := m.now().UTC()
	expiresAt := now.Add(accessTokenTTL)
	claims := AccessClaims{
		SessionID: sessionID,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    accessTokenIssuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{accessTokenAudience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发 access token 失败: %w", err)
	}
	return token, expiresAt, nil
}

func (m *TokenManager) VerifyAccessToken(raw string) (AccessClaims, error) {
	var claims AccessClaims
	token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		return m.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(accessTokenIssuer),
		jwt.WithAudience(accessTokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(m.now),
	)
	if err != nil || !token.Valid || claims.TokenType != "access" ||
		strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.SessionID) == "" {
		return AccessClaims{}, fmt.Errorf("access token 无效")
	}
	return claims, nil
}

func NewOpaqueToken() (raw string, hash [sha256.Size]byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", hash, fmt.Errorf("生成安全随机令牌失败: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func HashOpaqueToken(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}

func RefreshTokenExpiresAt(now time.Time) time.Time {
	return now.UTC().Add(refreshTokenTTL)
}

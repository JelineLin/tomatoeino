package account

import (
	"testing"
	"time"
)

func TestTokenManagerIssueAndVerify(t *testing.T) {
	m, err := NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	raw, expiresAt, err := m.IssueAccessToken("user-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !expiresAt.Equal(now.Add(accessTokenTTL)) {
		t.Fatalf("expiresAt = %v", expiresAt)
	}
	claims, err := m.VerifyAccessToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" || claims.SessionID != "session-1" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestTokenManagerRejectsWeakSecret(t *testing.T) {
	if _, err := NewTokenManager("short"); err == nil {
		t.Fatal("weak secret should be rejected")
	}
}

func TestOpaqueTokenHash(t *testing.T) {
	raw, hash, err := NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || hash != HashOpaqueToken(raw) {
		t.Fatal("opaque token/hash mismatch")
	}
}

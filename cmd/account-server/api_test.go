package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tomato-platform/internal/account"
)

type apiStore struct{}

func (apiStore) CreateChallenge(context.Context, string, [sha256.Size]byte, time.Time) (string, error) {
	return "challenge-1", nil
}
func (apiStore) LoginApple(context.Context, account.AppleLoginParams) (account.UserSession, error) {
	return account.UserSession{}, account.ErrInvalidChallenge
}
func (apiStore) RotateSession(context.Context, [sha256.Size]byte, [sha256.Size]byte, time.Time) (account.UserSession, error) {
	return account.UserSession{}, account.ErrInvalidSession
}
func (apiStore) UserForSession(context.Context, string, string) (account.User, error) {
	return account.User{}, account.ErrInvalidSession
}
func (apiStore) RevokeSession(context.Context, string, string) error {
	return account.ErrInvalidSession
}

type apiAppleVerifier struct{}

func (apiAppleVerifier) Verify(context.Context, string) (account.AppleIdentity, error) {
	return account.AppleIdentity{}, account.ErrInvalidAppleIdentity
}

func testAPIServer(t *testing.T) *server {
	t.Helper()
	tokens, err := account.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	return &server{account: account.NewService(apiStore{}, apiAppleVerifier{}, tokens)}
}

func TestAppleChallengeAPI(t *testing.T) {
	srv := testAPIServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/apple/challenges", strings.NewReader(`{"product_code":"menu"}`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d, cache=%q, body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	var response account.Challenge
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.ID != "challenge-1" || response.Nonce == "" {
		t.Fatalf("challenge = %+v, err = %v", response, err)
	}
}

func TestMeRequiresValidBearerToken(t *testing.T) {
	srv := testAPIServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIStrictJSONAndMethod(t *testing.T) {
	srv := testAPIServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/apple/challenges", strings.NewReader(`{"product_code":"menu","extra":true}`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("strict JSON status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/auth/refresh", nil)
	rec = httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("method response = %d, allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}

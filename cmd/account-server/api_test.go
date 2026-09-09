package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
func (apiStore) RequestDeletion(context.Context, string, string) (account.DeletionJob, error) {
	return account.DeletionJob{ID: "delete-1", Status: "pending"}, nil
}

type authorizedAPIStore struct{ apiStore }

func (authorizedAPIStore) UserForSession(_ context.Context, userID, sessionID string) (account.User, error) {
	return account.User{ID: userID, Products: []string{"menu"}}, nil
}

type apiAppleVerifier struct{}

func (apiAppleVerifier) Verify(context.Context, string) (account.AppleIdentity, error) {
	return account.AppleIdentity{}, account.ErrInvalidAppleIdentity
}

type apiAppleTokens struct{}

func (apiAppleTokens) ExchangeCode(context.Context, string, string, string) (account.AppleTokens, error) {
	return account.AppleTokens{}, account.ErrInvalidAppleAuthorization
}
func (apiAppleTokens) Revoke(context.Context, string, string) error { return nil }

func testAPIServer(t *testing.T) *server {
	t.Helper()
	tokens, err := account.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := account.NewTokenCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	return &server{account: account.NewService(apiStore{}, apiAppleVerifier{}, apiAppleTokens{}, cipher, tokens)}
}

func testAuthorizedAPIServer(t *testing.T) (*server, string) {
	t.Helper()
	tokens, err := account.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := account.NewTokenCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	accessToken, _, err := tokens.IssueAccessToken("user-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{account: account.NewService(authorizedAPIStore{}, apiAppleVerifier{}, apiAppleTokens{}, cipher, tokens)}
	return srv, accessToken
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

func TestDeleteMeQueuesDeletion(t *testing.T) {
	srv, accessToken := testAuthorizedAPIServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var job account.DeletionJob
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil || job.ID != "delete-1" || job.Status != "pending" {
		t.Fatalf("job=%+v err=%v", job, err)
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

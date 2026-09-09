package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tomato-platform/internal/account"
	"tomato-platform/internal/platformauth"
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

// testPlatformUserID 用规范 UUID：业务服务只把规范 UUID 当合法租户，
// 测试里的假身份也必须长成生产里的样子，否则契约测试测的是另一套东西。
const testPlatformUserID = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"

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
	accessToken, _, err := tokens.IssueAccessToken(testPlatformUserID, "session-1")
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

// GET /v1/me 是 Menu / English 唯一的鉴权依据，两边分属不同进程、不同包，
// 单测各测各的就会在字段名或状态码上悄悄分家。这里把真正的 platformauth 客户端
// 接到真正的账户路由上，一次锁住三件事：用户 ID 字段、产品权限字段、失效会话的状态码。
func TestMeIsTheContractProductServicesTrust(t *testing.T) {
	srv, accessToken := testAuthorizedAPIServer(t)
	upstream := httptest.NewServer(srv.routes())
	defer upstream.Close()

	menuClient, err := platformauth.NewClient(upstream.URL, "menu", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	userID, err := menuClient.Resolve(context.Background(), "Bearer "+accessToken)
	if err != nil || userID != testPlatformUserID {
		t.Fatalf("Resolve() = %q, %v", userID, err)
	}

	// 同一个会话在没开通的产品那边必须是 403，而不是被当成合法用户放进去。
	englishClient, err := platformauth.NewClient(upstream.URL, "english", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := englishClient.Resolve(context.Background(), "Bearer "+accessToken); !errors.Is(err, platformauth.ErrForbidden) {
		t.Fatalf("未开通产品应拒绝，得到 %v", err)
	}

	// 失效会话（登出/冻结/删除后）必须是 401——业务服务据此把用户踢回登录页。
	if _, err := menuClient.Resolve(context.Background(), "Bearer not-a-real-token"); !errors.Is(err, platformauth.ErrUnauthorized) {
		t.Fatalf("失效会话应 401，得到 %v", err)
	}

	// 身份响应不能被任何中间层缓存，否则登出在业务侧会「继续有效」。
	request, err := http.NewRequest(http.MethodGet, upstream.URL+"/v1/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := upstream.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header.Get("Cache-Control"))
	}
}

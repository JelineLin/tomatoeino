package account

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

type fakeAccountStore struct {
	challengeProduct string
	challengeHash    [sha256.Size]byte
	loginParams      AppleLoginParams
	oldRefreshHash   [sha256.Size]byte
	newRefreshHash   [sha256.Size]byte
	revokedUser      string
	revokedSession   string
}

func (f *fakeAccountStore) CreateChallenge(_ context.Context, product string, hash [sha256.Size]byte, _ time.Time) (string, error) {
	f.challengeProduct, f.challengeHash = product, hash
	return "challenge-1", nil
}

func (f *fakeAccountStore) LoginApple(_ context.Context, p AppleLoginParams) (UserSession, error) {
	f.loginParams = p
	return UserSession{User: User{ID: "user-1", Products: []string{p.ProductCode}}, SessionID: "session-1"}, nil
}

func (f *fakeAccountStore) RotateSession(_ context.Context, oldHash, newHash [sha256.Size]byte, _ time.Time) (UserSession, error) {
	f.oldRefreshHash, f.newRefreshHash = oldHash, newHash
	return UserSession{User: User{ID: "user-1", Products: []string{"menu"}}, SessionID: "session-1"}, nil
}

func (f *fakeAccountStore) UserForSession(_ context.Context, userID, sessionID string) (User, error) {
	if userID != "user-1" || sessionID != "session-1" {
		return User{}, ErrInvalidSession
	}
	return User{ID: userID, Products: []string{"menu"}}, nil
}

func (f *fakeAccountStore) RevokeSession(_ context.Context, userID, sessionID string) error {
	f.revokedUser, f.revokedSession = userID, sessionID
	return nil
}

type fakeAppleVerifier struct {
	identity AppleIdentity
	err      error
}

func (f fakeAppleVerifier) Verify(context.Context, string) (AppleIdentity, error) {
	return f.identity, f.err
}

func newTestService(t *testing.T, store AccountStore, verifier AppleIdentityVerifier) (*Service, *TokenManager) {
	t.Helper()
	tokens, err := NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	tokens.now = func() time.Time { return now }
	service := NewService(store, verifier, tokens)
	service.now = func() time.Time { return now }
	return service, tokens
}

func TestServiceChallengeAndLogin(t *testing.T) {
	store := &fakeAccountStore{}
	service, tokens := newTestService(t, store, fakeAppleVerifier{identity: AppleIdentity{
		Subject: "apple-user", Email: "user@example.com", EmailVerified: true, Nonce: "nonce-from-token",
	}})
	challenge, err := service.NewAppleChallenge(context.Background(), "menu")
	if err != nil {
		t.Fatal(err)
	}
	if challenge.ID != "challenge-1" || challenge.Nonce == "" || store.challengeProduct != "menu" ||
		store.challengeHash != HashOpaqueToken(challenge.Nonce) {
		t.Fatalf("challenge/store = (%+v, %+v)", challenge, store)
	}

	set, err := service.LoginApple(context.Background(), LoginInput{
		IdentityToken: "apple-token", ChallengeID: challenge.ID, ProductCode: "menu",
		DisplayName: " Parent\n", Device: Device{ID: "phone-1", Name: "iPhone", Platform: "ios"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.RefreshToken == "" || set.AccessToken == "" || set.User.ID != "user-1" {
		t.Fatalf("token set = %+v", set)
	}
	if store.loginParams.Identity.Subject != "apple-user" || store.loginParams.DisplayName != "Parent" ||
		store.loginParams.RefreshHash != HashOpaqueToken(set.RefreshToken) {
		t.Fatalf("login params = %+v", store.loginParams)
	}
	claims, err := tokens.VerifyAccessToken(set.AccessToken)
	if err != nil || claims.Subject != "user-1" || claims.SessionID != "session-1" {
		t.Fatalf("access claims = %+v, err = %v", claims, err)
	}
}

func TestServiceRefreshAuthenticateAndLogout(t *testing.T) {
	store := &fakeAccountStore{}
	service, tokens := newTestService(t, store, fakeAppleVerifier{})
	set, err := service.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if store.oldRefreshHash != HashOpaqueToken("old-refresh") ||
		store.newRefreshHash != HashOpaqueToken(set.RefreshToken) {
		t.Fatal("refresh token was not atomically rotated")
	}
	access, _, err := tokens.IssueAccessToken("user-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), access); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(context.Background(), access); err != nil {
		t.Fatal(err)
	}
	if store.revokedUser != "user-1" || store.revokedSession != "session-1" {
		t.Fatalf("revoked = (%s, %s)", store.revokedUser, store.revokedSession)
	}
}

func TestServiceRejectsInvalidInputAndAppleToken(t *testing.T) {
	service, _ := newTestService(t, &fakeAccountStore{}, fakeAppleVerifier{err: ErrInvalidAppleIdentity})
	if _, err := service.NewAppleChallenge(context.Background(), "bad"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("challenge error = %v", err)
	}
	_, err := service.LoginApple(context.Background(), LoginInput{
		IdentityToken: "bad", ChallengeID: "challenge", ProductCode: "menu", Device: Device{ID: "phone"},
	})
	if !errors.Is(err, ErrInvalidAppleIdentity) {
		t.Fatalf("login error = %v", err)
	}
}

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
	deletedUser      string
	deletionSession  string
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

func (f *fakeAccountStore) RequestDeletion(_ context.Context, userID, sessionID string) (DeletionJob, error) {
	f.deletedUser, f.deletionSession = userID, sessionID
	return DeletionJob{ID: "delete-1", Status: "pending"}, nil
}

type fakeAppleVerifier struct {
	identity AppleIdentity
	err      error
}

type sequenceAppleVerifier struct {
	identities []AppleIdentity
	index      int
}

func (f *sequenceAppleVerifier) Verify(context.Context, string) (AppleIdentity, error) {
	identity := f.identities[f.index]
	f.index++
	return identity, nil
}

func (f fakeAppleVerifier) Verify(context.Context, string) (AppleIdentity, error) {
	return f.identity, f.err
}

type fakeAppleTokenService struct {
	tokens AppleTokens
	err    error
}

func (f fakeAppleTokenService) ExchangeCode(context.Context, string, string, string) (AppleTokens, error) {
	return f.tokens, f.err
}

func (fakeAppleTokenService) Revoke(context.Context, string, string) error { return nil }

func newTestService(t *testing.T, store AccountStore, verifier AppleIdentityVerifier) (*Service, *TokenManager) {
	t.Helper()
	tokens, err := NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	tokens.now = func() time.Time { return now }
	cipher, err := NewTokenCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	appleTokens := fakeAppleTokenService{tokens: AppleTokens{
		RefreshToken: "apple-refresh", IdentityToken: "exchanged-identity-token",
	}}
	service := NewService(store, verifier, appleTokens, cipher, tokens)
	service.now = func() time.Time { return now }
	return service, tokens
}

func TestServiceChallengeAndLogin(t *testing.T) {
	store := &fakeAccountStore{}
	service, tokens := newTestService(t, store, fakeAppleVerifier{identity: AppleIdentity{
		Subject: "apple-user", ClientID: "com.example.menu", Email: "user@example.com",
		EmailVerified: true, Nonce: "nonce-from-token",
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
		IdentityToken: "apple-token", AuthorizationCode: "apple-code",
		ChallengeID: challenge.ID, ProductCode: "menu",
		DisplayName: " Parent\n", Device: Device{ID: "phone-1", Name: "iPhone", Platform: "ios"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.RefreshToken == "" || set.AccessToken == "" || set.User.ID != "user-1" {
		t.Fatalf("token set = %+v", set)
	}
	if store.loginParams.Identity.Subject != "apple-user" || store.loginParams.DisplayName != "Parent" ||
		store.loginParams.RefreshHash != HashOpaqueToken(set.RefreshToken) ||
		store.loginParams.AppleClientID != "com.example.menu" ||
		store.loginParams.AppleRefreshHash != HashOpaqueToken("apple-refresh") ||
		len(store.loginParams.AppleRefreshCiphertext) == 0 {
		t.Fatalf("login params = %+v", store.loginParams)
	}
	decrypted, err := service.cipher.OpenFor(
		appleCredentialAAD("apple-user", "com.example.menu"),
		store.loginParams.AppleRefreshCiphertext, store.loginParams.AppleRefreshNonce,
	)
	if err != nil || decrypted != "apple-refresh" {
		t.Fatalf("stored Apple credential = %q, err=%v", decrypted, err)
	}
	claims, err := tokens.VerifyAccessToken(set.AccessToken)
	if err != nil || claims.Subject != "user-1" || claims.SessionID != "session-1" {
		t.Fatalf("access claims = %+v, err = %v", claims, err)
	}
}

func TestServiceRejectsMismatchedExchangedIdentity(t *testing.T) {
	store := &fakeAccountStore{}
	verifier := &sequenceAppleVerifier{identities: []AppleIdentity{
		{Subject: "apple-user-1", ClientID: "com.example.menu", Nonce: "nonce"},
		{Subject: "apple-user-2", ClientID: "com.example.menu", Nonce: "nonce"},
	}}
	service, _ := newTestService(t, store, verifier)
	_, err := service.LoginApple(context.Background(), LoginInput{
		IdentityToken: "first", AuthorizationCode: "code", ChallengeID: "challenge",
		ProductCode: "menu", Device: Device{ID: "phone"},
	})
	if !errors.Is(err, ErrInvalidAppleAuthorization) {
		t.Fatalf("error=%v, want ErrInvalidAppleAuthorization", err)
	}
	if store.loginParams.ChallengeID != "" {
		t.Fatal("mismatched Apple credentials must not reach database login")
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
	job, err := service.RequestDeletion(context.Background(), access)
	if err != nil || job.ID != "delete-1" || store.deletedUser != "user-1" || store.deletionSession != "session-1" {
		t.Fatalf("deletion = %+v, deletedUser=%s, session=%s, err=%v", job, store.deletedUser, store.deletionSession, err)
	}
}

func TestServiceRejectsInvalidInputAndAppleToken(t *testing.T) {
	service, _ := newTestService(t, &fakeAccountStore{}, fakeAppleVerifier{err: ErrInvalidAppleIdentity})
	if _, err := service.NewAppleChallenge(context.Background(), "bad"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("challenge error = %v", err)
	}
	_, err := service.LoginApple(context.Background(), LoginInput{
		IdentityToken: "bad", AuthorizationCode: "code", ChallengeID: "challenge",
		ProductCode: "menu", Device: Device{ID: "phone"},
	})
	if !errors.Is(err, ErrInvalidAppleIdentity) {
		t.Fatalf("login error = %v", err)
	}
}

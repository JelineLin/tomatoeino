package account

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

type fakeDeletionStore struct {
	job       DeletionJob
	claimErr  error
	completed bool
	note      string
	retried   bool
	retryAt   time.Time
	retryMsg  string
}

func (f *fakeDeletionStore) ClaimDeletionJob(context.Context, time.Time) (DeletionJob, error) {
	return f.job, f.claimErr
}
func (f *fakeDeletionStore) CompleteDeletion(_ context.Context, _ DeletionJob, note string) error {
	f.completed, f.note = true, note
	return nil
}
func (f *fakeDeletionStore) RetryDeletion(_ context.Context, _ DeletionJob, retryAt time.Time, message string) error {
	f.retried, f.retryAt, f.retryMsg = true, retryAt, message
	return nil
}

type fakeRevoker struct {
	clientID string
	token    string
	err      error
}

func (f *fakeRevoker) ExchangeCode(context.Context, string, string, string) (AppleTokens, error) {
	return AppleTokens{}, errors.New("unused")
}
func (f *fakeRevoker) Revoke(_ context.Context, clientID, token string) error {
	f.clientID, f.token = clientID, token
	return f.err
}

func TestDeletionWorkerRevokesAppleThenCompletes(t *testing.T) {
	cipher := testDeletionCipher(t)
	subject, clientID := "apple-user", "com.example.menu"
	ciphertext, nonce, err := cipher.SealFor(appleCredentialAAD(subject, clientID), "apple-refresh")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeDeletionStore{job: DeletionJob{
		ID: "job-1", UserID: "user-1", AttemptCount: 1,
		Credentials: []AppleCredential{{Subject: subject, ClientID: clientID, Ciphertext: ciphertext, Nonce: nonce}},
	}}
	revoker := &fakeRevoker{}
	worker := NewDeletionWorker(store, revoker, cipher)
	handled, err := worker.ProcessOne(context.Background())
	if err != nil || !handled || !store.completed || store.retried {
		t.Fatalf("handled=%v completed=%v retried=%v err=%v", handled, store.completed, store.retried, err)
	}
	if revoker.clientID != "com.example.menu" || revoker.token != "apple-refresh" {
		t.Fatalf("revoked = (%s, %s)", revoker.clientID, revoker.token)
	}
}

func TestDeletionWorkerRetriesTemporaryFailure(t *testing.T) {
	cipher := testDeletionCipher(t)
	subject, clientID := "apple-user", "com.example.menu"
	ciphertext, nonce, _ := cipher.SealFor(appleCredentialAAD(subject, clientID), "apple-refresh")
	store := &fakeDeletionStore{job: DeletionJob{
		ID: "job-1", UserID: "user-1", AttemptCount: 3,
		Credentials: []AppleCredential{{Subject: subject, ClientID: clientID, Ciphertext: ciphertext, Nonce: nonce}},
	}}
	revoker := &fakeRevoker{err: ErrAppleUnavailable}
	worker := NewDeletionWorker(store, revoker, cipher)
	now := time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return now }
	handled, err := worker.ProcessOne(context.Background())
	if !handled || err == nil || !store.retried || store.completed {
		t.Fatalf("handled=%v completed=%v retried=%v err=%v", handled, store.completed, store.retried, err)
	}
	if want := now.Add(4 * time.Minute); !store.retryAt.Equal(want) {
		t.Fatalf("retryAt=%v, want %v", store.retryAt, want)
	}
}

func TestDeletionWorkerCompletesWhenAppleCredentialAlreadyInvalid(t *testing.T) {
	cipher := testDeletionCipher(t)
	subject, clientID := "apple-user", "com.example.menu"
	ciphertext, nonce, _ := cipher.SealFor(appleCredentialAAD(subject, clientID), "old-refresh")
	store := &fakeDeletionStore{job: DeletionJob{
		ID: "job-1", UserID: "user-1",
		Credentials: []AppleCredential{{Subject: subject, ClientID: clientID, Ciphertext: ciphertext, Nonce: nonce}},
	}}
	worker := NewDeletionWorker(store, &fakeRevoker{err: ErrInvalidAppleAuthorization}, cipher)
	if handled, err := worker.ProcessOne(context.Background()); err != nil || !handled || !store.completed {
		t.Fatalf("handled=%v completed=%v err=%v", handled, store.completed, err)
	}
	if store.note == "" {
		t.Fatal("manual revocation note should be retained")
	}
}

func TestDeletionWorkerNoJob(t *testing.T) {
	cipher := testDeletionCipher(t)
	worker := NewDeletionWorker(&fakeDeletionStore{claimErr: ErrNoDeletionJob}, &fakeRevoker{}, cipher)
	if handled, err := worker.ProcessOne(context.Background()); err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}

func testDeletionCipher(t *testing.T) *TokenCipher {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewTokenCipher(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

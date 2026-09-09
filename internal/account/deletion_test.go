package account

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
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

type fakePurger struct {
	product string
	calls   []string
	err     error
}

func (f *fakePurger) Product() string { return f.product }
func (f *fakePurger) Purge(_ context.Context, userID string) error {
	f.calls = append(f.calls, userID)
	return f.err
}

// 删除的先后顺序是这条链的命门：业务数据清干净之后，账户数据才可以硬删。
func TestDeletionWorkerPurgesProductsBeforeHardDelete(t *testing.T) {
	store := &fakeDeletionStore{job: DeletionJob{ID: "job-1", UserID: "user-1", AttemptCount: 1}}
	menu := &fakePurger{product: "menu"}
	english := &fakePurger{product: "english"}
	worker := NewDeletionWorker(store, &fakeRevoker{}, testDeletionCipher(t), menu, english)

	handled, err := worker.ProcessOne(context.Background())
	if !handled || err != nil {
		t.Fatalf("ProcessOne() = %v, %v", handled, err)
	}
	if len(menu.calls) != 1 || menu.calls[0] != "user-1" || len(english.calls) != 1 {
		t.Fatalf("两个产品都应被清除: menu=%v english=%v", menu.calls, english.calls)
	}
	if !store.completed {
		t.Fatal("产品清除成功后应完成账号删除")
	}
}

// 任一产品清不掉就整单重试，绝不能把账户先删了——那样业务数据会变成没人认领的孤儿。
func TestDeletionWorkerRetriesWhenProductPurgeFails(t *testing.T) {
	store := &fakeDeletionStore{job: DeletionJob{ID: "job-1", UserID: "user-1", AttemptCount: 2}}
	failing := &fakePurger{product: "english", err: errors.New("english 不可达")}
	worker := NewDeletionWorker(store, &fakeRevoker{}, testDeletionCipher(t), failing)

	handled, err := worker.ProcessOne(context.Background())
	if !handled || err == nil {
		t.Fatalf("ProcessOne() = %v, %v", handled, err)
	}
	if store.completed {
		t.Fatal("产品清除失败时绝不能硬删账户")
	}
	if !store.retried {
		t.Fatal("产品清除失败应排重试")
	}
	if !strings.Contains(store.retryMsg, "english") {
		t.Errorf("重试原因应留下是哪个产品失败了: %q", store.retryMsg)
	}
}

// 没配任何产品清除目标时删除照常完成（本地开发场景），但必须在任务备注里留痕。
func TestDeletionWorkerNotesMissingPurgeTargets(t *testing.T) {
	store := &fakeDeletionStore{job: DeletionJob{ID: "job-1", UserID: "user-1", AttemptCount: 1}}
	worker := NewDeletionWorker(store, &fakeRevoker{}, testDeletionCipher(t))

	if _, err := worker.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !store.completed {
		t.Fatal("没有产品目标时删除仍应完成")
	}
	if !strings.Contains(store.note, "no product purge target") {
		t.Errorf("备注应说明没有配置清除目标: %q", store.note)
	}
}

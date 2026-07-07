package migration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/syncstatus"
)

func TestServiceEncryptsPasswordBeforeEnqueue(t *testing.T) {
	t.Parallel()

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	codec, err := passwordcrypto.NewCodecFromBase64(key, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}
	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, codec)
	service.now = func() time.Time { return time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) }

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    []byte("cleartext-password"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Enqueued {
		t.Fatal("decision.Enqueued = false, want true")
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(queue.messages))
	}
	got := queue.messages[0]
	if got.Password != "" {
		t.Fatalf("queued Password = %q, want empty", got.Password)
	}
	if got.PasswordCiphertext == "" || got.PasswordNonce == "" || got.PasswordKeyID != "password-payload-key-v1" || got.PasswordAlg != passwordcrypto.AlgorithmAES256GCM {
		t.Fatalf("queued encrypted fields are invalid: %#v", got)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), "cleartext-password") || strings.Contains(string(body), `"password"`) {
		t.Fatalf("queued JSON leaks password: %s", body)
	}
}

func TestServiceZerosPasswordAfterSuccessfulEnqueue(t *testing.T) {
	t.Parallel()

	password := []byte("cleartext-password")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", &captureQueue{}, encrypter)
	service.now = func() time.Time { return time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC) }

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Enqueued {
		t.Fatal("decision.Enqueued = false, want true")
	}
	assertZeroedBytes(t, password, "request password after successful enqueue")
	assertZeroedBytes(t, encrypter.password, "encrypter borrowed password after successful enqueue")
}

func TestServiceZerosPasswordWhenSkippingExternalEmail(t *testing.T) {
	t.Parallel()

	password := []byte("external-password")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", &captureQueue{}, encrypter)

	decision, err := service.Submit(context.Background(), Request{
		CN:          "guest@gmail.com",
		EventType:   EventPasswordChange,
		Password:    password,
		DisplayName: "Guest",
		Mail:        "guest@gmail.com",
	})

	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !decision.Skipped {
		t.Fatal("decision.Skipped = false, want true")
	}
	if len(encrypter.password) != 0 {
		t.Fatalf("encrypter was called for skipped external identity")
	}
	assertZeroedBytes(t, password, "request password after external skip")
}

func TestServiceZerosPasswordWhenEncryptFails(t *testing.T) {
	t.Parallel()

	password := []byte("encrypt-failure-password")
	encryptErr := errors.New("encrypt failed")
	service := NewService("nycu.edu.tw", &captureQueue{}, failingEncrypter{err: encryptErr})

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if !errors.Is(err, encryptErr) {
		t.Fatalf("Submit error = %v, want encrypt error", err)
	}
	assertZeroedBytes(t, password, "request password after encrypt failure")
}

func TestServiceZerosPasswordWhenQueueFails(t *testing.T) {
	t.Parallel()

	password := []byte("queue-failure-password")
	queueErr := errors.New("queue unavailable")
	encrypter := &captureEncrypter{}
	service := NewService("nycu.edu.tw", failingQueue{err: queueErr}, encrypter)
	service.now = func() time.Time { return time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC) }

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    password,
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if !errors.Is(err, queueErr) {
		t.Fatalf("Submit error = %v, want queue error", err)
	}
	assertZeroedBytes(t, password, "request password after queue failure")
	assertZeroedBytes(t, encrypter.password, "encrypter borrowed password after queue failure")
}

type captureQueue struct {
	messages []PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}

type captureEncrypter struct {
	password []byte
}

func (e *captureEncrypter) Encrypt(_ context.Context, password []byte, _ []byte) (passwordcrypto.Envelope, error) {
	e.password = password
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}

type failingEncrypter struct {
	err error
}

func (e failingEncrypter) Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error) {
	return passwordcrypto.Envelope{}, e.err
}

type failingQueue struct {
	err error
}

func (q failingQueue) EnqueuePasswordSync(context.Context, PasswordSyncMessage) error {
	return q.err
}

type fakeSyncStatusStore struct {
	records        map[string]syncstatus.Record
	markPending    []markPendingCall
	getErr         error
	markPendingErr error
}

type markPendingCall struct {
	upn              string
	sourceEnqueuedAt time.Time
}

func newFakeSyncStatusStore() *fakeSyncStatusStore {
	return &fakeSyncStatusStore{records: make(map[string]syncstatus.Record)}
}

func (f *fakeSyncStatusStore) Get(_ context.Context, upn string) (syncstatus.Record, error) {
	if f.getErr != nil {
		return syncstatus.Record{}, f.getErr
	}
	return f.records[upn], nil
}

func (f *fakeSyncStatusStore) MarkPending(_ context.Context, upn string, sourceEnqueuedAt time.Time) error {
	f.markPending = append(f.markPending, markPendingCall{upn: upn, sourceEnqueuedAt: sourceEnqueuedAt})
	if f.markPendingErr != nil {
		return f.markPendingErr
	}
	f.records[upn] = syncstatus.Record{Status: syncstatus.StatusPending, UpdatedAt: time.Now(), SourceEnqueuedAt: sourceEnqueuedAt}
	return nil
}

func (f *fakeSyncStatusStore) setRecord(upn string, status syncstatus.Status, updatedAt time.Time) {
	f.records[upn] = syncstatus.Record{Status: status, UpdatedAt: updatedAt}
}

func TestServiceRejectsInvalidEventType(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{})

	_, err := service.Submit(context.Background(), Request{
		CN:        "311551001",
		EventType: EventType("bogus"),
		Password:  []byte("secret"),
	})

	if !errors.Is(err, ErrInvalidEventType) {
		t.Fatalf("Submit() error = %v, want ErrInvalidEventType", err)
	}
	if len(queue.messages) != 0 {
		t.Errorf("queue.messages = %d, want 0", len(queue.messages))
	}
}

func TestServiceSkipsLoginBootstrapWhenAlreadySynced(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Skipped {
		t.Error("decision.Skipped = false, want true (already-synced login_bootstrap should skip)")
	}
	if decision.Reason != "already_synced" {
		t.Errorf("decision.Reason = %q, want %q", decision.Reason, "already_synced")
	}
	if len(queue.messages) != 0 {
		t.Errorf("queue.messages = %d, want 0", len(queue.messages))
	}
}

func TestServiceSkipsLoginBootstrapWhenPendingFresh(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store, PendingTTL: 5 * time.Minute})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusPending, time.Now().Add(-1*time.Minute))

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Skipped {
		t.Error("decision.Skipped = false, want true (fresh pending sync should skip)")
	}
	if decision.Reason != "sync_pending" {
		t.Errorf("decision.Reason = %q, want %q", decision.Reason, "sync_pending")
	}
	if len(queue.messages) != 0 {
		t.Errorf("queue.messages = %d, want 0", len(queue.messages))
	}
}

func TestServiceEnqueuesLoginBootstrapWhenPendingStale(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store, PendingTTL: 5 * time.Minute})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusPending, time.Now().Add(-10*time.Minute))

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (stale pending sync should re-enqueue)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceEnqueuesLoginBootstrapAfterSyncFailed(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusFailed, time.Now())

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (a previously failed sync must re-enqueue on next login)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceAlwaysEnqueuesPasswordChangeEvenWhenSynced(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordChange,
		Password:    []byte("newsecret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (password_change always resyncs)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceAlwaysEnqueuesPasswordRecoveryEvenWhenSynced(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordRecovery,
		Password:    []byte("recoveredsecret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (password_recovery always resyncs)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceMarksPendingWithSourceEnqueuedAtAfterSuccessfulEnqueue(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})
	fixedNow := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if len(store.markPending) != 1 {
		t.Fatalf("store.markPending = %d calls, want 1", len(store.markPending))
	}
	call := store.markPending[0]
	if call.upn != "311551001@nycu.edu.tw" {
		t.Errorf("markPending upn = %q, want %q", call.upn, "311551001@nycu.edu.tw")
	}
	if !call.sourceEnqueuedAt.Equal(fixedNow) {
		t.Errorf("markPending sourceEnqueuedAt = %v, want %v (must equal msg.EnqueuedAt)", call.sourceEnqueuedAt, fixedNow)
	}
}

func TestServiceEnqueuedMessageIncludesEventType(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{})

	_, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventPasswordRecovery,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queue.messages = %d, want 1", len(queue.messages))
	}
	if queue.messages[0].EventType != EventPasswordRecovery {
		t.Errorf("queued EventType = %q, want %q", queue.messages[0].EventType, EventPasswordRecovery)
	}
}

func TestServiceNilSyncStatusStoreBehavesLikeNoDedupe(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{})

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (nil store must not block enqueue)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceDecisionUPNPopulatedOnSkip(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	upn := "311551001@nycu.edu.tw"
	store.setRecord(upn, syncstatus.StatusSynced, time.Now())

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Skipped {
		t.Fatal("decision.Skipped = false, want true")
	}
	if decision.UPN != upn {
		t.Errorf("decision.UPN = %q, want %q (UPN must be populated even on a skip)", decision.UPN, upn)
	}
}

func TestServiceSkipLoginBootstrapFailsOpenOnStoreGetError(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	store.getErr = errors.New("store unavailable")
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true (a sync-status Get failure must fail open and still enqueue)")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func TestServiceEnqueueSucceedsWhenMarkPendingFails(t *testing.T) {
	t.Parallel()

	queue := &captureQueue{}
	store := newFakeSyncStatusStore()
	store.markPendingErr = errors.New("store unavailable")
	service := NewService("nycu.edu.tw", queue, &captureEncrypter{}, ServiceOptions{SyncStatusStore: store})

	decision, err := service.Submit(context.Background(), Request{
		CN:          "311551001",
		EventType:   EventLoginBootstrap,
		Password:    []byte("secret"),
		DisplayName: "Student",
		Mail:        "student@nycu.edu.tw",
	})

	if err != nil {
		t.Fatalf("Submit() error = %v, want nil (a MarkPending failure must never fail an otherwise-successful enqueue)", err)
	}
	if !decision.Enqueued {
		t.Error("decision.Enqueued = false, want true")
	}
	if len(queue.messages) != 1 {
		t.Errorf("queue.messages = %d, want 1", len(queue.messages))
	}
}

func assertZeroedBytes(t *testing.T, buf []byte, context string) {
	t.Helper()
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("%s byte %d = %d, want 0", context, i, b)
		}
	}
}

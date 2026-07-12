package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/azuremonitor"
	"github.com/nycu/password-hook-service/internal/config"
	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/observability"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/syncstatus"
	"github.com/nycu/password-hook-service/internal/worker"
)

const (
	testServiceBusConnectionString = "servicebus-connection-string-for-tests"
	allowedPortalRemoteAddr        = "192.0.2.10:12345"
)

func TestAppHookRouteEnqueuesInternalIdentity(t *testing.T) {
	logs, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.Header.Set("X-Request-ID", "trace-123")
	signRequest(req, cfg.HMACSecret, body)
	req.RemoteAddr = allowedPortalRemoteAddr
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(queue.messages))
	}
	if queue.messages[0].UPN != "311551001@nycu.edu.tw" {
		t.Fatalf("queued UPN = %q", queue.messages[0].UPN)
	}
	if !bytes.Contains(logs.Bytes(), []byte(`"traceId":"trace-123"`)) {
		t.Fatalf("logs missing traceId: %s", logs.String())
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatalf("logs leaked password: %s", logs.String())
	}
}

func TestNewRequiresPasswordEncryptionConfig(t *testing.T) {
	cfg := completeAppConfig()
	cfg.PasswordEncryptionKeyB64 = ""

	application, err := NewWithQueue(cfg, &captureQueue{})
	if err == nil {
		t.Fatal("NewWithQueue returned nil error")
	}
	if application != nil {
		t.Fatalf("NewWithQueue application = %#v, want nil", application)
	}
	if err.Error() != "PASSWORD_ENCRYPTION_KEY_B64 is required" {
		t.Fatalf("NewWithQueue error = %q, want PASSWORD_ENCRYPTION_KEY_B64 is required", err.Error())
	}
}

func TestNewWithQueueRequiresPortalAllowedCIDRs(t *testing.T) {
	cfg := completeAppConfig()
	cfg.PortalAllowedCIDRs = nil

	application, err := NewWithQueue(cfg, &captureQueue{})

	if err == nil {
		t.Fatal("NewWithQueue returned nil error")
	}
	if application != nil {
		t.Fatalf("NewWithQueue application = %#v, want nil", application)
	}
	if err.Error() != "PORTAL_ALLOWED_CIDRS is required" {
		t.Fatalf("NewWithQueue error = %q, want PORTAL_ALLOWED_CIDRS is required", err.Error())
	}
}

func TestAppRejectsNonAllowlistedSourceBeforeHMAC(t *testing.T) {
	logs, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	req.RemoteAddr = "198.51.100.10:12345"
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "source ip is not allowed") {
		t.Fatalf("body = %q, want source ip is not allowed", rec.Body.String())
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatalf("logs leaked password: %s", logs.String())
	}
}

func TestAppRateLimitsBeforeHMAC(t *testing.T) {
	queue := &captureQueue{}
	cfg := completeAppConfig()
	cfg.RateLimitPerIP = 1
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	firstReq.RemoteAddr = allowedPortalRemoteAddr
	first := httptest.NewRecorder()
	application.ServeHTTP(first, firstReq)

	if first.Code != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want %d: %s", first.Code, http.StatusUnauthorized, first.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	secondReq.RemoteAddr = allowedPortalRemoteAddr
	second := httptest.NewRecorder()
	application.ServeHTTP(second, secondReq)

	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d: %s", second.Code, http.StatusTooManyRequests, second.Body.String())
	}
}

func TestAppRejectsOversizedHookBody(t *testing.T) {
	queue := &captureQueue{}
	cfg := completeAppConfig()
	cfg.HookMaxBodyBytes = 8
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	signRequest(req, cfg.HMACSecret, body)
	req.RemoteAddr = allowedPortalRemoteAddr
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestAppHookRouteQueuesCiphertextOnlyMessage(t *testing.T) {
	_, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"cleartext-password","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	signRequest(req, cfg.HMACSecret, body)
	req.RemoteAddr = allowedPortalRemoteAddr
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(queue.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(queue.messages))
	}
	got := queue.messages[0]
	if got.Password != "" {
		t.Fatalf("queued Password = %q, want empty", got.Password)
	}
	if got.PasswordCiphertext == "" || got.PasswordNonce == "" || got.PasswordKeyID != "password-payload-key-v1" || got.PasswordAlg == "" {
		t.Fatalf("queued encrypted fields are invalid: %#v", got)
	}
	queuedJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal queued message returned error: %v", err)
	}
	if bytes.Contains(queuedJSON, []byte("cleartext-password")) || bytes.Contains(queuedJSON, []byte(`"password"`)) {
		t.Fatalf("queued message leaks cleartext password: %s", queuedJSON)
	}
}

func TestAppHookRouteSkipsExternalEmailWithoutEnqueue(t *testing.T) {
	_, restore := captureDefaultLogger()
	defer restore()

	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := NewWithQueue(cfg, queue)
	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}

	body := []byte(`{"cn":"abc@gmail.com","password":"secret","displayName":"Guest","mail":"abc@gmail.com","eventType":"login_bootstrap"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	signRequest(req, cfg.HMACSecret, body)
	req.RemoteAddr = allowedPortalRemoteAddr
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(queue.messages) != 0 {
		t.Fatalf("queued %d messages, want 0", len(queue.messages))
	}
}

func TestNewWithQueueClosesOwnedQueueWhenAppWiringFails(t *testing.T) {
	cfg := completeAppConfig()
	cfg.HMACSecret = ""
	closer := &captureCloser{}

	application, err := newWithQueue(cfg, &captureQueue{}, mustPasswordCodec(t, cfg), nil, closer)
	if err == nil {
		t.Fatal("newWithQueue returned nil error")
	}
	if application != nil {
		t.Fatalf("newWithQueue returned app = %#v, want nil", application)
	}
	if closer.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closer.closeCalls)
	}
	if len(closer.closeContexts) != 1 {
		t.Fatalf("close contexts = %d, want 1", len(closer.closeContexts))
	}
	if closer.closeContexts[0] == nil {
		t.Fatal("close context is nil")
	}
}

func TestNewWithQueueDoesNotRequireServiceBusConfiguration(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusQueueName = ""
	cfg.GraphTenantID = ""
	cfg.GraphClientID = ""
	cfg.GraphClientSecret = ""

	application, err := NewWithQueue(cfg, &captureQueue{})

	if err != nil {
		t.Fatalf("NewWithQueue returned error: %v", err)
	}
	if application == nil {
		t.Fatal("NewWithQueue returned nil app")
	}
}

func TestAzureMonitorObservabilityRuntimeWiresRecorderAndShutdown(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ObservabilityExporter = config.ObservabilityExporterAzureMonitor
	cfg.OTLPExporterEndpoint = "http://localhost:4318"
	cfg.AzureMonitorMetricResourceID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/password-hook"
	cfg.AzureMonitorMetricRegion = "eastasia"
	cfg.AzureMonitorMetricNamespace = "password-hook-service"

	runtime, err := newObservabilityRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newObservabilityRuntime returned error: %v", err)
	}
	if _, ok := runtime.recorder.(*azuremonitor.MetricRecorder); !ok {
		t.Fatalf("recorder = %T, want *azuremonitor.MetricRecorder", runtime.recorder)
	}
	if len(runtime.closers) < 2 {
		t.Fatalf("closers = %d, want OTel shutdown and metric flush closers", len(runtime.closers))
	}
	if err := closeAppResources(context.Background(), runtime.closers); err != nil {
		t.Fatalf("close observability runtime returned error: %v", err)
	}
}

func TestNewWithQueueUsesConfiguredRecorder(t *testing.T) {
	recorder := observability.NewCaptureRecorder()
	queue := &captureQueue{}
	cfg := completeAppConfig()
	application, err := newWithQueueWithRecorder(cfg, queue, mustPasswordCodec(t, cfg), syncstatus.NewMemoryStore(), recorder)
	if err != nil {
		t.Fatalf("newWithQueueWithRecorder returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"secret","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	signRequest(req, cfg.HMACSecret, body)
	req.RemoteAddr = allowedPortalRemoteAddr
	response := httptest.NewRecorder()

	application.ServeHTTP(response, req)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if samples := recorder.Counters(observability.MetricHookRequestsTotal); len(samples) != 1 {
		t.Fatalf("hook request samples = %d, want 1", len(samples))
	}
}

func TestNewRequiresGraphCredentialsInFullMode(t *testing.T) {
	cfg := completeAppConfig()
	cfg.GraphClientSecret = ""

	application, err := New(cfg)

	if err == nil {
		t.Fatal("New returned nil error")
	}
	if application != nil {
		t.Fatalf("New application = %#v, want nil", application)
	}
	if err.Error() != "GRAPH_CLIENT_SECRET is required" {
		t.Fatalf("New error = %q, want GRAPH_CLIENT_SECRET is required", err.Error())
	}
}

func TestRunStartsWorkerAndHTTPServer(t *testing.T) {
	cfg := completeAppConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	receiver := newBlockingReceiver()
	application, err := newWithWorkerDependencies(cfg, &captureQueue{}, receiver, &captureProcessor{}, &captureDeadLetterSink{}, mustPasswordCodec(t, cfg))
	if err != nil {
		t.Fatalf("newWithWorkerDependencies returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	select {
	case <-receiver.started:
	case <-time.After(time.Second):
		t.Fatal("worker receiver was not started")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestNewWithWorkerDependenciesSharesPasswordCodecWithHookAndWorker(t *testing.T) {
	_, restore := captureDefaultLogger()
	defer restore()

	cfg := completeAppConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	codec := &capturePasswordCodec{plaintext: []byte("worker-password")}
	receiver := newSingleMessageReceiver(passwordSyncWorkerMessage(t))
	processor := &captureProcessor{}
	application, err := newWithWorkerDependencies(cfg, &captureQueue{}, receiver, processor, &captureDeadLetterSink{}, codec)
	if err != nil {
		t.Fatalf("newWithWorkerDependencies returned error: %v", err)
	}

	body := []byte(`{"cn":"311551001","password":"hook-password","displayName":"Student","mail":"student@nycu.edu.tw","eventType":"login_bootstrap"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hook/password", bytes.NewReader(body))
	signRequest(req, cfg.HMACSecret, body)
	req.RemoteAddr = allowedPortalRemoteAddr
	rec := httptest.NewRecorder()

	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if codec.encryptCalls != 1 {
		t.Fatalf("codec encrypt calls = %d, want 1", codec.encryptCalls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	processor.onProcess = cancel
	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	select {
	case <-receiver.completed:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("worker message was not completed")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Run did not return after worker processed message")
	}
	if codec.decryptCalls != 1 {
		t.Fatalf("codec decrypt calls = %d, want 1", codec.decryptCalls)
	}
	if len(processor.passwords) != 1 || string(processor.passwords[0]) != "worker-password" {
		t.Fatalf("processor passwords = %q, want [worker-password]", processor.passwords)
	}
}

func TestRunClosesQueueWithBoundedContextFromCallerContext(t *testing.T) {
	closer := &captureCloser{}
	cfg := completeAppConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	application, err := newWithQueue(cfg, &captureQueue{}, mustPasswordCodec(t, cfg), nil, closer)
	if err != nil {
		t.Fatalf("newWithQueue returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if closer.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closer.closeCalls)
	}
	if err := closer.closeErrs[0]; err != nil {
		t.Fatalf("close context err = %v, want nil", err)
	}
	if !closer.closeHadDeadlines[0] {
		t.Fatal("close context has no deadline")
	}
}

func TestRunClosesAllOwnedResources(t *testing.T) {
	senderCloser := &captureCloser{}
	receiverCloser := &captureCloser{}
	dlqCloser := &captureCloser{}
	cfg := completeAppConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	application, err := newWithWorkerDependencies(
		cfg,
		&captureQueue{},
		newBlockingReceiver(),
		&captureProcessor{},
		&captureDeadLetterSink{},
		mustPasswordCodec(t, cfg),
		senderCloser,
		receiverCloser,
		dlqCloser,
	)
	if err != nil {
		t.Fatalf("newWithWorkerDependencies returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	for name, closer := range map[string]*captureCloser{
		"sender":   senderCloser,
		"receiver": receiverCloser,
		"dlq":      dlqCloser,
	} {
		if closer.closeCalls != 1 {
			t.Fatalf("%s close calls = %d, want 1", name, closer.closeCalls)
		}
		if err := closer.closeErrs[0]; err != nil {
			t.Fatalf("%s close context err = %v, want nil", name, err)
		}
		if !closer.closeHadDeadlines[0] {
			t.Fatalf("%s close context has no deadline", name)
		}
	}
}

func TestPeriodicMetricFlusherFlushesWhileAppRuns(t *testing.T) {
	flusher := newCaptureMetricFlusher()
	closer := newPeriodicMetricFlusher(flusher, 5*time.Millisecond)

	select {
	case <-flusher.calls:
	case <-time.After(time.Second):
		t.Fatal("metric flusher was not called before shutdown")
	}

	if err := closer.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	callsAfterClose := flusher.count.Load()
	time.Sleep(20 * time.Millisecond)
	if got := flusher.count.Load(); got != callsAfterClose {
		t.Fatalf("flush calls after close = %d, want %d", got, callsAfterClose)
	}
}

type captureQueue struct {
	messages []migration.PasswordSyncMessage
}

func (q *captureQueue) EnqueuePasswordSync(_ context.Context, msg migration.PasswordSyncMessage) error {
	q.messages = append(q.messages, msg)
	return nil
}

type captureCloser struct {
	closeCalls        int
	closeContexts     []context.Context
	closeErrs         []error
	closeHadDeadlines []bool
}

func (c *captureCloser) Close(ctx context.Context) error {
	c.closeCalls++
	c.closeContexts = append(c.closeContexts, ctx)
	c.closeErrs = append(c.closeErrs, ctx.Err())
	_, hasDeadline := ctx.Deadline()
	c.closeHadDeadlines = append(c.closeHadDeadlines, hasDeadline)
	return nil
}

type captureMetricFlusher struct {
	count atomic.Int64
	calls chan struct{}
}

func newCaptureMetricFlusher() *captureMetricFlusher {
	return &captureMetricFlusher{calls: make(chan struct{}, 10)}
}

func (f *captureMetricFlusher) Flush(context.Context) error {
	f.count.Add(1)
	select {
	case f.calls <- struct{}{}:
	default:
	}
	return nil
}

type blockingReceiver struct {
	started chan struct{}
}

func newBlockingReceiver() *blockingReceiver {
	return &blockingReceiver{started: make(chan struct{})}
}

func (r *blockingReceiver) ReceiveMessages(ctx context.Context, _ int) ([]*worker.Message, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *blockingReceiver) CompleteMessage(context.Context, *worker.Message) error {
	return nil
}

func (r *blockingReceiver) AbandonMessage(context.Context, *worker.Message) error {
	return nil
}

type singleMessageReceiver struct {
	msg       *worker.Message
	delivered bool
	completed chan struct{}
}

func newSingleMessageReceiver(msg *worker.Message) *singleMessageReceiver {
	return &singleMessageReceiver{msg: msg, completed: make(chan struct{})}
}

func (r *singleMessageReceiver) ReceiveMessages(ctx context.Context, _ int) ([]*worker.Message, error) {
	if !r.delivered {
		r.delivered = true
		return []*worker.Message{r.msg}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *singleMessageReceiver) CompleteMessage(context.Context, *worker.Message) error {
	select {
	case <-r.completed:
	default:
		close(r.completed)
	}
	return nil
}

func (r *singleMessageReceiver) AbandonMessage(context.Context, *worker.Message) error {
	return nil
}

type captureProcessor struct {
	onProcess func()
	passwords [][]byte
}

func (p *captureProcessor) ProcessPasswordSync(_ context.Context, cmd worker.PasswordSyncCommand) error {
	p.passwords = append(p.passwords, append([]byte(nil), cmd.Password...))
	if p.onProcess != nil {
		p.onProcess()
	}
	return nil
}

type captureDeadLetterSink struct{}

func (s *captureDeadLetterSink) RecordPasswordSyncFailure(context.Context, worker.DeadLetterEntry) error {
	return nil
}

type capturePasswordCodec struct {
	encryptCalls int
	decryptCalls int
	plaintext    []byte
}

func (c *capturePasswordCodec) Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error) {
	c.encryptCalls++
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}

func (c *capturePasswordCodec) Decrypt(context.Context, passwordcrypto.Envelope, []byte) ([]byte, error) {
	c.decryptCalls++
	return append([]byte(nil), c.plaintext...), nil
}

func passwordSyncWorkerMessage(t *testing.T) *worker.Message {
	t.Helper()
	body, err := json.Marshal(migration.PasswordSyncMessage{
		CN:                 "311551001",
		UPN:                "311551001@nycu.edu.tw",
		PasswordCiphertext: "ciphertext",
		PasswordNonce:      "nonce",
		PasswordKeyID:      "password-payload-key-v1",
		PasswordAlg:        passwordcrypto.AlgorithmAES256GCM,
		EnqueuedAt:         time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal worker message: %v", err)
	}
	return &worker.Message{Kind: "password-sync", Body: body}
}

func mustPasswordCodec(t *testing.T, cfg config.Config) *passwordcrypto.Codec {
	t.Helper()
	codec, err := passwordcrypto.NewCodecFromBase64(cfg.PasswordEncryptionKeyB64, cfg.PasswordEncryptionKeyID)
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}
	return codec
}

func completeAppConfig() config.Config {
	return config.Config{
		SecretsSource:                 config.SecretsSourceEnv,
		KeyVaultURL:                   "",
		KeyVaultSecretNames:           config.KeyVaultSecretNames{HMACSecret: "hook-hmac-secret", ServiceBusConnectionString: "servicebus-conn-str", GraphClientSecret: "graph-client-secret", PasswordEncryptionKey: "password-payload-encryption-key"},
		HTTPAddr:                      ":8080",
		HMACSecret:                    "shared-secret",
		EntraPrimaryDomain:            "nycu.edu.tw",
		EntraFallbackDomain:           "nycu.onmicrosoft.com",
		ProblemBaseURL:                "https://nycu.edu.tw/problems",
		HMACClockSkew:                 30 * time.Second,
		NonceTTL:                      60 * time.Second,
		PortalAllowedCIDRs:            []string{"192.0.2.0/24"},
		RateLimitPerIP:                500,
		RateLimitWindow:               time.Second,
		HookMaxBodyBytes:              64 * 1024,
		ServiceBusAuthMode:            config.ServiceBusAuthConnectionString,
		ServiceBusNamespaceFQDN:       "",
		ServiceBusConnectionString:    testServiceBusConnectionString,
		ServiceBusQueueName:           "password-sync",
		ServiceBusDeadLetterQueueName: "password-sync-dlq",
		PasswordMessageTTL:            300 * time.Second,
		PasswordEncryptionKeyB64:      base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		PasswordEncryptionKeyID:       "password-payload-key-v1",
		GraphTenantID:                 "tenant-id",
		GraphClientID:                 "client-id",
		GraphClientSecret:             "graph-client-secret",
	}
}

func captureDefaultLogger() (*bytes.Buffer, func()) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	return &logs, func() {
		slog.SetDefault(previous)
	}
}

func signRequest(req *http.Request, secret string, body []byte) {
	timestamp := time.Now().Unix()
	nonce := "0123456789abcdef0123456789abcdef"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s.", timestamp, nonce)))
	_, _ = mac.Write(body)

	req.Header.Set("X-Hook-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Hook-Nonce", nonce)
	req.Header.Set("X-Hook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
}

type captureServiceBusRuntimeBuilder struct {
	authMode         string
	namespaceFQDN    string
	connectionString string
	closers          []appCloser
}

func (b *captureServiceBusRuntimeBuilder) build(cfg config.Config) (serviceBusRuntime, error) {
	b.authMode = cfg.ServiceBusAuthMode
	b.namespaceFQDN = cfg.ServiceBusNamespaceFQDN
	b.connectionString = cfg.ServiceBusConnectionString
	return serviceBusRuntime{
		queue:    &captureQueue{},
		receiver: newBlockingReceiver(),
		dlq:      &captureDeadLetterSink{},
		closers:  b.closers,
	}, nil
}

func replaceServiceBusRuntimeBuilder(fn func(config.Config) (serviceBusRuntime, error)) func() {
	original := buildServiceBusRuntime
	buildServiceBusRuntime = fn
	return func() {
		buildServiceBusRuntime = original
	}
}

func TestNewUsesManagedIdentityServiceBusRuntime(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ServiceBusAuthMode = config.ServiceBusAuthManagedIdentity
	cfg.ServiceBusConnectionString = ""
	cfg.ServiceBusNamespaceFQDN = "nycu-password-hook.servicebus.windows.net"
	cfg.GraphTenantID = "00000000-0000-0000-0000-000000000001"
	cfg.GraphClientID = "00000000-0000-0000-0000-000000000002"
	builder := &captureServiceBusRuntimeBuilder{}
	restore := replaceServiceBusRuntimeBuilder(builder.build)
	defer restore()

	application, err := New(cfg)

	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if application == nil {
		t.Fatal("New returned nil application")
	}
	if builder.authMode != config.ServiceBusAuthManagedIdentity {
		t.Fatalf("auth mode = %q, want managed_identity", builder.authMode)
	}
	if builder.namespaceFQDN != "nycu-password-hook.servicebus.windows.net" {
		t.Fatalf("namespace = %q", builder.namespaceFQDN)
	}
	if builder.connectionString != "" {
		t.Fatalf("connection string = %q, want empty", builder.connectionString)
	}
}

func TestNewUsesConnectionStringServiceBusRuntime(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ServiceBusAuthMode = config.ServiceBusAuthConnectionString
	cfg.ServiceBusNamespaceFQDN = ""
	cfg.GraphTenantID = "00000000-0000-0000-0000-000000000001"
	cfg.GraphClientID = "00000000-0000-0000-0000-000000000002"
	builder := &captureServiceBusRuntimeBuilder{}
	restore := replaceServiceBusRuntimeBuilder(builder.build)
	defer restore()

	application, err := New(cfg)

	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if application == nil {
		t.Fatal("New returned nil application")
	}
	if builder.authMode != config.ServiceBusAuthConnectionString {
		t.Fatalf("auth mode = %q, want connection_string", builder.authMode)
	}
	if builder.connectionString != cfg.ServiceBusConnectionString {
		t.Fatalf("connection string = %q, want config value", builder.connectionString)
	}
}

func TestNewClosesServiceBusRuntimeWhenLaterWiringFails(t *testing.T) {
	cfg := completeAppConfig()
	cfg.PasswordEncryptionKeyB64 = "not-base64"
	cfg.GraphTenantID = "00000000-0000-0000-0000-000000000001"
	cfg.GraphClientID = "00000000-0000-0000-0000-000000000002"
	closer := &captureCloser{}
	builder := &captureServiceBusRuntimeBuilder{closers: []appCloser{closer}}
	restore := replaceServiceBusRuntimeBuilder(builder.build)
	defer restore()

	application, err := New(cfg)

	if err == nil {
		t.Fatal("New returned nil error")
	}
	if application != nil {
		t.Fatalf("New application = %#v, want nil", application)
	}
	if closer.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closer.closeCalls)
	}
}

func TestNewClosesObservabilityRuntimeWhenServiceBusRuntimeFails(t *testing.T) {
	cfg := completeAppConfig()
	cfg.ObservabilityExporter = config.ObservabilityExporterAzureMonitor
	cfg.OTLPExporterEndpoint = "http://localhost:4318"
	cfg.AzureMonitorMetricResourceID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/password-hook"
	cfg.AzureMonitorMetricRegion = "eastasia"
	cfg.AzureMonitorMetricNamespace = "password-hook-service"
	restore := replaceServiceBusRuntimeBuilder(func(config.Config) (serviceBusRuntime, error) {
		return serviceBusRuntime{}, fmt.Errorf("service bus runtime build failed")
	})
	defer restore()

	application, err := New(cfg)

	if err == nil {
		t.Fatal("New returned nil error")
	}
	if application != nil {
		t.Fatalf("New application = %#v, want nil", application)
	}
}

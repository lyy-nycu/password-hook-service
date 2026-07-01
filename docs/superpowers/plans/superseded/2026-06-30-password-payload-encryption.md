# Password Payload Encryption Implementation Plan

> **Plan Status:** Superseded
>
> **Do Not Execute:** Use `docs/superpowers/plans/active/2026-07-01-password-payload-encryption-realignment.md` instead.
>
> **Historical Value:** This file remains design input for the realignment plan and ADR.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure Azure Service Bus and native DLQ never store cleartext passwords by encrypting password payloads at the application layer before enqueue and decrypting only inside the worker immediately before Microsoft Graph processing.

**Architecture:** Add an application-level password encryption boundary between `internal/migration` and `internal/servicebusqueue`. The hook service receives the portal password in memory, encrypts it into an authenticated ciphertext envelope, and queues only ciphertext. The worker decodes the encrypted queue message, decrypts the password per processing attempt, calls the processor/Graph path, and clears plaintext before retry backoff or settlement.

**Tech Stack:** Go 1.26, standard-library `crypto/aes`, `crypto/cipher`, `crypto/rand`, `encoding/base64`, existing Azure Service Bus SDK, existing Key Vault secret loader.

---

## Security Invariants

- Service Bus message body must never contain the original cleartext password.
- Service Bus application properties must never contain the password or ciphertext.
- Native Service Bus DLQ must not retain the original password sync body after terminal failures.
- Safe DLQ records may contain `cn`, `upn`, reason, attempts, and timestamps only.
- Hook service plaintext lifetime target: under 1 second from HTTP decode to encryption.
- Worker plaintext lifetime target: one Graph attempt timeout only, with no plaintext retained during retry backoff.
- Backoff/retry state may retain ciphertext, but not cleartext.
- Go memory zeroing is best effort; implementation must still avoid logging, formatting, or storing password strings beyond the processing call.

## File Structure

- Modify: `docs/superpowers/specs/2026-06-24-password-hook-service-design.md` - document the revised security model and queue/DLQ invariants.
- Create: `internal/passwordcrypto/codec.go` - AES-256-GCM password envelope encryption/decryption.
- Create: `internal/passwordcrypto/codec_test.go` - encryption, authentication failure, validation, and no-plaintext tests.
- Modify: `internal/config/config.go` - add password encryption key configuration and Key Vault secret name.
- Modify: `internal/config/config_test.go` - cover env/keyvault config validation for the encryption key.
- Modify: `internal/secretloader/loader.go` - resolve the password encryption key from Key Vault.
- Modify: `internal/secretloader/loader_test.go` - cover Key Vault resolution and sanitized failures.
- Modify: `internal/app/app.go` - wire the encrypting migration service.
- Modify: `internal/app/app_test.go` - verify app construction requires encryption config in full mode.
- Modify: `internal/migration/message.go` - replace JSON password field with ciphertext envelope fields.
- Modify: `internal/migration/service.go` - encrypt request password before enqueue.
- Create or modify: `internal/migration/service_test.go` - prove enqueue receives ciphertext-only messages.
- Modify: `internal/servicebusqueue/queue.go` - preserve ciphertext-only body and remove native DLQ from the password sync worker path.
- Modify: `internal/servicebusqueue/queue_test.go` - assert no cleartext password is serialized.
- Modify: `internal/worker/worker.go` - decrypt per processing attempt, clear plaintext before settlement/backoff, and use safe DLQ.
- Modify: `internal/worker/worker_test.go` - cover decrypt/process lifecycle, retry without holding plaintext, and safe DLQ behavior.

## Message Schema

Replace the queued JSON shape from this:

```json
{
  "cn": "311551001",
  "upn": "311551001@nycu.edu.tw",
  "password": "cleartext_password",
  "displayName": "王大明",
  "mail": "wang@nycu.edu.tw",
  "enqueuedAt": "2026-06-30T00:00:00Z"
}
```

to this:

```json
{
  "cn": "311551001",
  "upn": "311551001@nycu.edu.tw",
  "passwordCiphertext": "base64...",
  "passwordNonce": "base64...",
  "passwordKeyId": "password-payload-key-v1",
  "passwordAlg": "AES-256-GCM",
  "displayName": "王大明",
  "mail": "wang@nycu.edu.tw",
  "enqueuedAt": "2026-06-30T00:00:00Z"
}
```

`migration.PasswordSyncMessage` must keep `Password string` only as an in-memory field for worker-to-processor handoff:

```go
type PasswordSyncMessage struct {
	CN                 string    `json:"cn"`
	UPN                string    `json:"upn"`
	Password           string    `json:"-"`
	PasswordCiphertext string    `json:"passwordCiphertext"`
	PasswordNonce      string    `json:"passwordNonce"`
	PasswordKeyID      string    `json:"passwordKeyId"`
	PasswordAlg        string    `json:"passwordAlg"`
	DisplayName        string    `json:"displayName"`
	Mail               string    `json:"mail"`
	EnqueuedAt         time.Time `json:"enqueuedAt"`
}
```

---

## Task 1: Update The Design Spec

**Files:**
- Modify: `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`

- [ ] **Step 1: Replace the Service Bus password protection language**

Update the architecture/security sections so they no longer imply Service Bus TTL plus encryption at rest is enough. Add this exact invariant:

```markdown
Password queue payloads are application-level encrypted before enqueue. Azure Service Bus stores only authenticated ciphertext, nonce, algorithm, and key id. Service Bus encryption at rest remains enabled, but it is treated as storage-layer protection, not password-field protection.
```

- [ ] **Step 2: Update the happy path**

Change the flow to:

```markdown
7. Hook Service: classify cn
   -> student ID / employee ID: encrypt password into an AES-256-GCM envelope and build Service Bus message {cn, upn, passwordCiphertext, passwordNonce, passwordKeyId, passwordAlg, displayName, mail, ttl=300s}
   -> external email: do not enqueue; log/metric skipped_external_identity; respond 202
...
11. Worker: decrypt password only for the current Graph attempt
12. Worker: GET /v1.0/users/{upn} -> Graph API
    -> 404 Not Found: POST /v1.0/users (create account + set password)
    -> 200 OK: PATCH /v1.0/users/{upn} (update password only)
13. Worker: zero plaintext password buffer before retry backoff or settlement
```

- [ ] **Step 3: Update DLQ language**

Add:

```markdown
Native Service Bus DLQ is not used for password sync payloads. Terminal failures are recorded in an application-level safe DLQ message containing only cn, upn, reason, attempts, and timestamps. The original password sync message is completed after the safe DLQ write succeeds.
```

- [ ] **Step 4: Commit**

Run:

```bash
git add docs/superpowers/specs/2026-06-24-password-hook-service-design.md
git commit -m "docs: require encrypted password queue payloads"
```

---

## Task 2: Add Password Envelope Encryption

**Files:**
- Create: `internal/passwordcrypto/codec.go`
- Create: `internal/passwordcrypto/codec_test.go`

- [ ] **Step 1: Write failing tests**

Create tests for:

```go
func TestCodecEncryptsWithoutPlaintextAndDecrypts(t *testing.T)
func TestCodecRejectsWrongKey(t *testing.T)
func TestNewCodecRejectsInvalidKey(t *testing.T)
func TestDecryptRejectsWrongAlgorithm(t *testing.T)
```

The first test must marshal the envelope to JSON and assert the original password is absent:

```go
if strings.Contains(string(body), "cleartext-password") {
	t.Fatalf("encrypted envelope contains plaintext: %s", body)
}
```

- [ ] **Step 2: Confirm failure**

Run:

```bash
go test ./internal/passwordcrypto -v
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement codec**

Implement:

```go
package passwordcrypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const AlgorithmAES256GCM = "AES-256-GCM"

type Envelope struct {
	Ciphertext string
	Nonce      string
	KeyID      string
	Algorithm  string
}

type Codec struct {
	aead  cipher.AEAD
	keyID string
}

func NewCodecFromBase64(keyB64 string, keyID string) (*Codec, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("decode password encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("password encryption key must decode to 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	if keyID == "" {
		return nil, errors.New("password encryption key id is required")
	}
	return &Codec{aead: aead, keyID: keyID}, nil
}

func (c *Codec) Encrypt(ctx context.Context, plaintext []byte, aad []byte) (Envelope, error) {
	if err := ctx.Err(); err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate password nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, aad)
	return Envelope{
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		KeyID:      c.keyID,
		Algorithm:  AlgorithmAES256GCM,
	}, nil
}

func (c *Codec) Decrypt(ctx context.Context, env Envelope, aad []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if env.Algorithm != AlgorithmAES256GCM {
		return nil, fmt.Errorf("unsupported password encryption algorithm %q", env.Algorithm)
	}
	if env.KeyID != c.keyID {
		return nil, fmt.Errorf("unsupported password encryption key id %q", env.KeyID)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode password nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode password ciphertext: %w", err)
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, errors.New("decrypt password payload failed")
	}
	return plaintext, nil
}

func ZeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
```

- [ ] **Step 4: Verify and commit**

Run:

```bash
go test ./internal/passwordcrypto -v
git add internal/passwordcrypto/codec.go internal/passwordcrypto/codec_test.go
git commit -m "feat: add password payload encryption codec"
```

Expected: PASS.

---

## Task 3: Add Encryption Configuration And Secret Loading

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/secretloader/loader.go`
- Modify: `internal/secretloader/loader_test.go`

- [ ] **Step 1: Write failing config tests**

Add tests that expect:

```go
cfg := config.Load()
if cfg.KeyVaultSecretNames.PasswordEncryptionKey != "password-payload-encryption-key" {
	t.Fatalf("unexpected password key secret name: %q", cfg.KeyVaultSecretNames.PasswordEncryptionKey)
}
```

and:

```go
cfg := config.Config{
	SecretsSource: "env",
	HMACSecret: "hmac",
	EntraPrimaryDomain: "nycu.edu.tw",
	ProblemBaseURL: "https://nycu.edu.tw/problems",
	ServiceBusConnectionString: "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=name;SharedAccessKey=key",
	ServiceBusQueueName: "password-sync",
	PasswordMessageTTL: time.Minute,
	PasswordEncryptionKeyB64: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	PasswordEncryptionKeyID: "password-payload-key-v1",
}
if err := cfg.Validate(); err != nil {
	t.Fatalf("Validate returned error: %v", err)
}
```

- [ ] **Step 2: Implement config fields**

Add:

```go
type KeyVaultSecretNames struct {
	HMACSecret                 string
	ServiceBusConnectionString string
	GraphClientSecret          string
	PasswordEncryptionKey      string
}

type Config struct {
	...
	PasswordEncryptionKeyB64 string
	PasswordEncryptionKeyID  string
}
```

Load env:

```go
PasswordEncryptionKey: env("KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME", "password-payload-encryption-key"),
PasswordEncryptionKeyB64: strings.TrimSpace(os.Getenv("PASSWORD_ENCRYPTION_KEY_B64")),
PasswordEncryptionKeyID: env("PASSWORD_ENCRYPTION_KEY_ID", "password-payload-key-v1"),
```

Validate full app mode:

```go
case strings.TrimSpace(c.PasswordEncryptionKeyB64) == "":
	return errors.New("PASSWORD_ENCRYPTION_KEY_B64 is required")
case strings.TrimSpace(c.PasswordEncryptionKeyID) == "":
	return errors.New("PASSWORD_ENCRYPTION_KEY_ID is required")
```

- [ ] **Step 3: Implement Key Vault resolution**

In `resolveKeyVault`, load the encryption key secret and assign:

```go
passwordEncryptionKey, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.PasswordEncryptionKey)
if err != nil {
	return config.Config{}, err
}
cfg.PasswordEncryptionKeyB64 = passwordEncryptionKey
```

- [ ] **Step 4: Verify and commit**

Run:

```bash
go test ./internal/config ./internal/secretloader -v
git add internal/config/config.go internal/config/config_test.go internal/secretloader/loader.go internal/secretloader/loader_test.go
git commit -m "feat: load password encryption key config"
```

Expected: PASS.

---

## Task 4: Encrypt Before Enqueue

**Files:**
- Modify: `internal/migration/message.go`
- Modify: `internal/migration/service.go`
- Create or modify: `internal/migration/service_test.go`

- [ ] **Step 1: Write failing migration tests**

Add `TestServiceEncryptsPasswordBeforeEnqueue`:

```go
func TestServiceEncryptsPasswordBeforeEnqueue(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	codec, err := passwordcrypto.NewCodecFromBase64(key, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}
	queue := &captureQueue{}
	service := NewService("nycu.edu.tw", queue, codec)

	_, err = service.Submit(context.Background(), Request{
		CN: "311551001",
		Password: "cleartext-password",
		DisplayName: "Test User",
		Mail: "test@nycu.edu.tw",
	})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	got := queue.message
	if got.Password != "" {
		t.Fatalf("queued in-memory password = %q, want empty", got.Password)
	}
	if got.PasswordCiphertext == "" || got.PasswordNonce == "" {
		t.Fatalf("queued encrypted fields are empty: %#v", got)
	}
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), "cleartext-password") || strings.Contains(string(body), `"password"`) {
		t.Fatalf("queued message leaks password: %s", body)
	}
}
```

- [ ] **Step 2: Update schema**

Use the `PasswordSyncMessage` schema from this plan's Message Schema section.

- [ ] **Step 3: Update service constructor and submit path**

Change constructor:

```go
type PasswordEncrypter interface {
	Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error)
}

type Service struct {
	primaryDomain string
	queue         Queue
	encrypter     PasswordEncrypter
	now           func() time.Time
}

func NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter) *Service
```

Build AAD from stable identity metadata:

```go
func passwordAAD(cn string, upn string, enqueuedAt time.Time) []byte {
	return []byte(strings.TrimSpace(cn) + "\x00" + strings.TrimSpace(upn) + "\x00" + enqueuedAt.UTC().Format(time.RFC3339Nano))
}
```

Encrypt before enqueue:

```go
passwordBytes := []byte(req.Password)
defer passwordcrypto.ZeroBytes(passwordBytes)
env, err := s.encrypter.Encrypt(ctx, passwordBytes, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
if err != nil {
	return decision, err
}
msg.PasswordCiphertext = env.Ciphertext
msg.PasswordNonce = env.Nonce
msg.PasswordKeyID = env.KeyID
msg.PasswordAlg = env.Algorithm
```

- [ ] **Step 4: Verify and commit**

Run:

```bash
go test ./internal/migration -v
git add internal/migration/message.go internal/migration/service.go internal/migration/service_test.go
git commit -m "feat: encrypt password before queue enqueue"
```

Expected: PASS.

---

## Task 5: Enforce Ciphertext-Only Service Bus Serialization

**Files:**
- Modify: `internal/servicebusqueue/queue.go`
- Modify: `internal/servicebusqueue/queue_test.go`

- [ ] **Step 1: Update failing queue tests**

Change `TestQueueSendsPasswordSyncMessageWithTTL` so the input message contains:

```go
msg := migration.PasswordSyncMessage{
	CN: "u1234567",
	UPN: "u1234567@example.edu",
	Password: "must-not-be-serialized",
	PasswordCiphertext: "ciphertext",
	PasswordNonce: "nonce",
	PasswordKeyID: "password-payload-key-v1",
	PasswordAlg: "AES-256-GCM",
	DisplayName: "Test User",
	Mail: "test@example.edu",
	EnqueuedAt: enqueuedAt,
}
```

Assert:

```go
if strings.Contains(string(got.Body), "must-not-be-serialized") || strings.Contains(string(got.Body), `"password"`) {
	t.Fatalf("Service Bus body leaks password: %s", got.Body)
}
if strings.Contains(fmt.Sprint(got.ApplicationProperties), "must-not-be-serialized") {
	t.Fatalf("Service Bus metadata leaks password: %#v", got.ApplicationProperties)
}
```

- [ ] **Step 2: Keep queue adapter simple**

`Queue.EnqueuePasswordSync` should still marshal `migration.PasswordSyncMessage`; the `json:"-"` tag on `Password` enforces no plaintext serialization.

- [ ] **Step 3: Verify and commit**

Run:

```bash
go test ./internal/servicebusqueue -run TestQueueSendsPasswordSyncMessageWithTTL -v
git add internal/servicebusqueue/queue.go internal/servicebusqueue/queue_test.go
git commit -m "test: enforce ciphertext-only service bus payload"
```

Expected: PASS.

---

## Task 6: Decrypt Only During Worker Processing

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: Write failing worker tests**

Add:

```go
func TestWorkerDecryptsPasswordForProcessorAndClearsAfterCall(t *testing.T)
func TestWorkerDoesNotKeepPlaintextDuringRetryBackoff(t *testing.T)
func TestWorkerRejectsMessageMissingEncryptedPasswordFields(t *testing.T)
func TestWorkerPermanentDecryptFailureUsesSafeDLQAndCompletesOriginal(t *testing.T)
```

The fake decrypter should expose active plaintext count:

```go
type fakeDecrypter struct {
	plaintext []byte
	active int32
}

func (d *fakeDecrypter) Decrypt(ctx context.Context, env passwordcrypto.Envelope, aad []byte) ([]byte, error) {
	atomic.AddInt32(&d.active, 1)
	return append([]byte(nil), d.plaintext...), nil
}
```

The fake processor should assert it receives the expected password and then the worker zeros the byte slice after the call.

- [ ] **Step 2: Add decrypter dependency**

Add:

```go
type PasswordDecrypter interface {
	Decrypt(context.Context, passwordcrypto.Envelope, []byte) ([]byte, error)
}
```

Extend `Options`:

```go
type Options struct {
	MaxMessages       int
	SettlementTimeout time.Duration
	EmptyReceiveDelay time.Duration
	PasswordDecrypter PasswordDecrypter
	DeadLetterSink    DeadLetterSink
	RetryBackoffs     []time.Duration
	Now               func() time.Time
	Sleep             func(context.Context, time.Duration) error
}
```

`New` must reject nil `PasswordDecrypter` with:

```text
worker password decrypter is required
```

- [ ] **Step 3: Decode encrypted messages**

Update validation:

```go
if strings.TrimSpace(out.PasswordCiphertext) == "" {
	return migration.PasswordSyncMessage{}, errors.New("password sync message passwordCiphertext is required")
}
if strings.TrimSpace(out.PasswordNonce) == "" {
	return migration.PasswordSyncMessage{}, errors.New("password sync message passwordNonce is required")
}
if strings.TrimSpace(out.PasswordKeyID) == "" {
	return migration.PasswordSyncMessage{}, errors.New("password sync message passwordKeyId is required")
}
if strings.TrimSpace(out.PasswordAlg) == "" {
	return migration.PasswordSyncMessage{}, errors.New("password sync message passwordAlg is required")
}
```

- [ ] **Step 4: Decrypt per attempt**

Use this processing shape:

```go
plaintext, err := w.passwordDecrypter.Decrypt(ctx, passwordcrypto.Envelope{
	Ciphertext: msg.PasswordCiphertext,
	Nonce: msg.PasswordNonce,
	KeyID: msg.PasswordKeyID,
	Algorithm: msg.PasswordAlg,
}, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
if err != nil {
	return &PermanentError{Reason: PermanentReasonProcessorError, Err: err}
}
defer passwordcrypto.ZeroBytes(plaintext)

msg.Password = string(plaintext)
err = w.processor.ProcessPasswordSync(ctx, msg)
msg.Password = ""
passwordcrypto.ZeroBytes(plaintext)
```

The implementation must zero `plaintext` before sleeping for retry backoff.

- [ ] **Step 5: Verify and commit**

Run:

```bash
go test ./internal/worker -v
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: decrypt password only inside worker attempt"
```

Expected: PASS.

---

## Task 7: Replace Native DLQ With Safe DLQ For Password Sync

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`
- Create: `internal/servicebusqueue/deadletter.go`
- Create: `internal/servicebusqueue/deadletter_test.go`
- Modify: `internal/servicebusqueue/queue.go`
- Modify: `internal/servicebusqueue/queue_test.go`

- [ ] **Step 1: Write failing tests**

Add worker tests for:

```go
func TestWorkerPermanentProcessorErrorWritesSafeDLQAndCompletesOriginal(t *testing.T)
func TestWorkerInvalidMessageWritesSafeDLQAndCompletesOriginal(t *testing.T)
func TestWorkerSafeDLQFailureAbandonsOriginal(t *testing.T)
```

Assert safe DLQ JSON does not contain:

```go
[]string{`"password"`, "cleartext-password", "passwordCiphertext", "ciphertext"}
```

- [ ] **Step 2: Remove native DLQ from password worker receiver**

Change `worker.Receiver`:

```go
type Receiver interface {
	ReceiveMessages(context.Context, int) ([]*Message, error)
	CompleteMessage(context.Context, *Message) error
	AbandonMessage(context.Context, *Message) error
}
```

Remove `DeadLetterMessage` from `internal/servicebusqueue.Receiver` and its tests.

- [ ] **Step 3: Add safe DLQ sink**

Create `internal/servicebusqueue/deadletter.go`:

```go
type DeadLetterQueue struct {
	sender sender
	client closer
}

func (q *DeadLetterQueue) RecordPasswordSyncFailure(ctx context.Context, entry worker.DeadLetterEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal safe dead-letter entry: %w", err)
	}
	if bytes.Contains(body, []byte(`"password"`)) {
		return errors.New("safe dead-letter entry contains password field")
	}
	contentType := "application/json"
	subject := "password-sync-failure"
	msg := &azservicebus.Message{
		ApplicationProperties: map[string]any{
			"kind": "password-sync-failure",
			"cn": entry.CN,
			"upn": entry.UPN,
			"reason": entry.Reason,
		},
		Body: body,
		ContentType: &contentType,
		Subject: &subject,
	}
	if err := q.sender.SendMessage(ctx, msg, nil); err != nil {
		return fmt.Errorf("send safe dead-letter entry: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Verify and commit**

Run:

```bash
go test ./internal/worker ./internal/servicebusqueue -v
git add internal/worker/worker.go internal/worker/worker_test.go internal/servicebusqueue/queue.go internal/servicebusqueue/queue_test.go internal/servicebusqueue/deadletter.go internal/servicebusqueue/deadletter_test.go
git commit -m "feat: use password-safe application dlq"
```

Expected: PASS.

---

## Task 8: Wire App Construction

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing app tests**

Add:

```go
func TestNewRequiresPasswordEncryptionKey(t *testing.T)
func TestNewWithQueueWiresEncryptingMigrationService(t *testing.T)
```

- [ ] **Step 2: Wire codec**

In `New` and `NewWithQueue`, construct:

```go
passwordCodec, err := passwordcrypto.NewCodecFromBase64(cfg.PasswordEncryptionKeyB64, cfg.PasswordEncryptionKeyID)
if err != nil {
	return nil, err
}
service := migration.NewService(cfg.EntraPrimaryDomain, queue, passwordCodec)
```

- [ ] **Step 3: Verify and commit**

Run:

```bash
go test ./internal/app -v
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat: wire password payload encryption"
```

Expected: PASS.

---

## Task 9: End-To-End Regression Checks

**Files:**
- Modify tests only if verification exposes gaps.

- [ ] **Step 1: Run full unit suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Search for plaintext queue password serialization**

Run:

```bash
rg -n 'json:"password"|DeadLetterMessage|password sync message password is required|Password:.*json' internal
```

Expected:

- No `json:"password"` in `migration.PasswordSyncMessage`.
- No native `DeadLetterMessage` in the password sync worker path.
- No worker validation requiring plaintext `Password`.

- [ ] **Step 3: Search docs for stale claims**

Run:

```bash
rg -n 'Service Bus encryption at rest|password excluded|message TTL|password.*, ttl=300s|Dead-Letter' docs/superpowers/specs/2026-06-24-password-hook-service-design.md docs/superpowers/plans
```

Expected: any remaining references distinguish storage-layer encryption from application-level password encryption.

- [ ] **Step 4: Commit final verification fixes**

If verification required small corrections:

```bash
git add <changed-files>
git commit -m "test: verify encrypted password queue boundary"
```

---

## Operational Notes

- Generate `PASSWORD_ENCRYPTION_KEY_B64` from 32 random bytes:

```bash
openssl rand -base64 32
```

- Store it in Key Vault under `password-payload-encryption-key`.
- Restrict Key Vault secret read access to the hook service and worker managed identities only.
- Restrict Service Bus receive permissions to the worker identity only.
- A Service Bus reader without the Key Vault secret can read only ciphertext.
- A Key Vault secret reader without Service Bus receive access cannot recover queued passwords.
- Key rotation should be additive first: deploy support for multiple key ids before retiring old ciphertext-bearing messages.

## Self-Review

- Spec coverage: the plan updates docs, message schema, encryption, config, queue serialization, worker decryption, safe DLQ, and verification.
- Placeholder scan: no unfinished placeholder markers are present.
- Type consistency: `PasswordSyncMessage` carries ciphertext fields for JSON and `Password string` as `json:"-"`; worker decrypts into the in-memory field only.
- Scope check: this is one cohesive hardening change. It does not implement the real Microsoft Graph client beyond preserving the existing processor interface.

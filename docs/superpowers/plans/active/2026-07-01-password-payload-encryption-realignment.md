# Password Payload Encryption Realignment Implementation Plan

> **Plan Status:** Completed
>
> **Use For:** Current implementation work before continuing to the Microsoft Graph slice.
>
> **Do Not Use For:** General historical context after this realignment is completed; move it to `completed/` when done.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Realign the completed producer/worker/DLQ slices with ADR 2026-07-01 so Azure Service Bus and native DLQ never store cleartext password payloads.

**Architecture:** Add an application-level AES-256-GCM password encryption boundary before `internal/servicebusqueue` and decrypt only inside `internal/worker` for the current Graph processing attempt. Replace native Service Bus dead-lettering in the password sync worker path with an application-level safe DLQ record that excludes both plaintext and ciphertext. Keep existing HMAC, classification, UPN, and HTTP behavior intact unless they directly depend on the password queue schema.

**Tech Stack:** Go 1.26, standard-library `crypto/aes`, `crypto/cipher`, `crypto/rand`, `encoding/base64`, existing Azure Service Bus SDK, existing Key Vault secret loader, existing fake-based Go tests.

---

## Scoped Recheck Findings

The spec/story change affects only the password queue boundary and terminal failure handling. It does not require reworking HMAC validation, rate limiting, request routing, identity classification, or UPN resolution.

Current implementation mismatches:

- `internal/migration/message.go` still serializes `Password string` as `json:"password"`.
- `internal/migration/service.go` still copies `req.Password` directly into the queued message.
- `internal/servicebusqueue/queue.go` still marshals the plaintext-bearing message body.
- `internal/worker/worker.go` still validates that queued messages contain plaintext `Password`.
- `internal/worker/worker.go` and `internal/servicebusqueue/queue.go` still expose native `DeadLetterMessage` in the password sync path.
- `internal/config/config.go` and `internal/secretloader/loader.go` do not load a password payload encryption key.
- `internal/app/app.go` wires `migration.NewService` without an encryption dependency.

Historical slice impact:

| Slice | Previous State | Realignment |
|---|---|---|
| Slice 2 Producer to Service Bus | Done with plaintext JSON queue body | Keep queue adapter shape, replace body schema with ciphertext-only password envelope |
| Slice 3 Secret Loading | Done without password payload key | Add Key Vault/env loading for encryption key and key id |
| Slice 4 Worker Queue Consumption | Done with plaintext decode and native DLQ API | Keep receiver/process loop shape, decode encrypted schema and remove native DLQ from password path |
| Slice 5 Retry and DLQ Policy | Planned around safe DLQ but still references plaintext message validation | Fold into this realignment so safe DLQ and decrypt-per-attempt are implemented together |
| Slice 7 Password Data Protection | Not planned | Pull forward the queue encryption and worker plaintext lifetime parts because they are now required before Graph work |

## Superseded Plan Notes

- `docs/superpowers/plans/completed/2026-06-25-slice-02-producer-servicebus.md` remains historical for Service Bus sender setup, TTL, config, and app injection patterns. Its plaintext queue payload assumptions are superseded by this plan.
- `docs/superpowers/plans/completed/2026-06-27-slice-04-worker-queue-consumption.md` remains historical for worker loop and receiver adapter patterns. Its plaintext message schema and native DLQ assumptions are superseded by this plan.
- `docs/superpowers/plans/superseded/2026-06-29-slice-05-retry-dlq-policy.md` is superseded for execution by this plan. Its safe DLQ direction is retained, but password payload encryption changes the worker schema and invalid-message validation.
- `docs/superpowers/plans/superseded/2026-06-30-password-payload-encryption.md` is superseded for execution by this realignment plan because this plan incorporates the scoped implementation audit and ADR.

## File Structure

- Create: `internal/passwordcrypto/codec.go` - AES-256-GCM envelope encryption/decryption and byte zeroing helper.
- Create: `internal/passwordcrypto/codec_test.go` - encryption, decryption, authentication failure, validation, and no-plaintext tests.
- Modify: `internal/config/config.go` - add password payload key config, key id config, and safe DLQ queue config.
- Modify: `internal/config/config_test.go` - cover defaults and validation for encryption and safe DLQ config.
- Modify: `internal/secretloader/loader.go` - load password payload encryption key from Key Vault.
- Modify: `internal/secretloader/loader_test.go` - cover password key secret resolution and sanitized failures.
- Modify: `internal/migration/message.go` - replace serialized password field with ciphertext envelope fields; keep `Password string` as `json:"-"` for in-memory handoff.
- Modify: `internal/migration/service.go` - require an encrypter, encrypt before enqueue, and clear plaintext byte slices.
- Modify: `internal/migration/service_test.go` - prove queued messages contain ciphertext only.
- Modify: `internal/servicebusqueue/queue.go` - preserve ciphertext-only body, enforce password-free application properties, and remove native DLQ from receiver contract.
- Modify: `internal/servicebusqueue/queue_test.go` - assert Service Bus body/properties never contain plaintext password.
- Create: `internal/servicebusqueue/deadletter.go` - Service Bus sender-backed application safe DLQ sink.
- Create: `internal/servicebusqueue/deadletter_test.go` - safe DLQ serialization tests.
- Modify: `internal/worker/worker.go` - decode encrypted messages, decrypt per attempt, zero plaintext before retry backoff/settlement, retry transient failures, and write safe DLQ on terminal failures.
- Modify: `internal/worker/worker_test.go` - cover decrypt lifecycle, retries, safe DLQ, settlement, and no plaintext/ciphertext leak.
- Modify: `internal/app/app.go` - wire password codec into migration service and worker dependencies.
- Modify: `internal/app/app_test.go` - verify full-mode construction requires encryption config and hook route queues ciphertext-only messages.
- Modify: `docs/superpowers/plans/roadmap.md` - mark this realignment as active and superseding old execution plans.

---

## Task 1: Add Password Crypto Codec

**Files:**
- Create: `internal/passwordcrypto/codec.go`
- Create: `internal/passwordcrypto/codec_test.go`

- [x] **Step 1: Write failing codec tests**

Create `internal/passwordcrypto/codec_test.go`:

```go
package passwordcrypto

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestCodecEncryptsWithoutPlaintextAndDecrypts(t *testing.T) {
	t.Parallel()

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	codec, err := NewCodecFromBase64(key, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}

	env, err := codec.Encrypt(context.Background(), []byte("cleartext-password"), []byte("cn=u123;upn=u123@example.edu"))
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), "cleartext-password") {
		t.Fatalf("encrypted envelope contains plaintext: %s", body)
	}
	if env.Ciphertext == "" || env.Nonce == "" || env.KeyID != "password-payload-key-v1" || env.Algorithm != AlgorithmAES256GCM {
		t.Fatalf("unexpected envelope: %#v", env)
	}

	plaintext, err := codec.Decrypt(context.Background(), env, []byte("cn=u123;upn=u123@example.edu"))
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if string(plaintext) != "cleartext-password" {
		t.Fatalf("plaintext = %q, want cleartext-password", plaintext)
	}
}

func TestCodecRejectsWrongKey(t *testing.T) {
	t.Parallel()

	keyA := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	keyB := base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789"))
	codecA, err := NewCodecFromBase64(keyA, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 A returned error: %v", err)
	}
	codecB, err := NewCodecFromBase64(keyB, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 B returned error: %v", err)
	}

	env, err := codecA.Encrypt(context.Background(), []byte("cleartext-password"), []byte("aad"))
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	if _, err := codecB.Decrypt(context.Background(), env, []byte("aad")); err == nil || err.Error() != "decrypt password payload failed" {
		t.Fatalf("Decrypt error = %v, want decrypt password payload failed", err)
	}
}

func TestNewCodecRejectsInvalidKey(t *testing.T) {
	t.Parallel()

	if _, err := NewCodecFromBase64(base64.StdEncoding.EncodeToString([]byte("short")), "password-payload-key-v1"); err == nil {
		t.Fatal("NewCodecFromBase64 returned nil error for short key")
	}
	if _, err := NewCodecFromBase64("not-base64", "password-payload-key-v1"); err == nil {
		t.Fatal("NewCodecFromBase64 returned nil error for invalid base64")
	}
	if _, err := NewCodecFromBase64(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), ""); err == nil || err.Error() != "password encryption key id is required" {
		t.Fatalf("NewCodecFromBase64 error = %v, want key id required", err)
	}
}

func TestDecryptRejectsWrongAlgorithm(t *testing.T) {
	t.Parallel()

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	codec, err := NewCodecFromBase64(key, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}

	env, err := codec.Encrypt(context.Background(), []byte("cleartext-password"), nil)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	env.Algorithm = "AES-128-CBC"

	if _, err := codec.Decrypt(context.Background(), env, nil); err == nil || !strings.Contains(err.Error(), "unsupported password encryption algorithm") {
		t.Fatalf("Decrypt error = %v, want unsupported algorithm", err)
	}
}

func TestZeroBytesClearsBuffer(t *testing.T) {
	t.Parallel()

	buf := []byte("cleartext-password")
	ZeroBytes(buf)
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("buf[%d] = %d, want 0", i, b)
		}
	}
}
```

- [x] **Step 2: Confirm failure**

Run:

```bash
go test ./internal/passwordcrypto -v
```

Expected: FAIL because `internal/passwordcrypto` does not exist.

- [x] **Step 3: Implement codec**

Create `internal/passwordcrypto/codec.go`:

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
	if keyID == "" {
		return nil, errors.New("password encryption key id is required")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
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

func ZeroBytes(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
```

- [x] **Step 4: Verify and commit**

Run:

```bash
go test ./internal/passwordcrypto -v
```

Expected: PASS.

Commit:

```bash
git add internal/passwordcrypto/codec.go internal/passwordcrypto/codec_test.go
git commit -m "feat: add password payload encryption codec"
```

## Task 2: Add Encryption Key Configuration And Secret Loading

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/secretloader/loader.go`
- Modify: `internal/secretloader/loader_test.go`

- [x] **Step 1: Write failing config tests**

Add tests covering:

```go
func TestLoadDefaultsPasswordEncryptionConfig(t *testing.T) {
	t.Parallel()

	cfg := Load()

	if cfg.KeyVaultSecretNames.PasswordEncryptionKey != "password-payload-encryption-key" {
		t.Fatalf("PasswordEncryptionKey secret name = %q", cfg.KeyVaultSecretNames.PasswordEncryptionKey)
	}
	if cfg.PasswordEncryptionKeyID != "password-payload-key-v1" {
		t.Fatalf("PasswordEncryptionKeyID = %q", cfg.PasswordEncryptionKeyID)
	}
	if cfg.ServiceBusDeadLetterQueueName != "password-sync-dlq" {
		t.Fatalf("ServiceBusDeadLetterQueueName = %q", cfg.ServiceBusDeadLetterQueueName)
	}
}

func TestValidateRequiresPasswordEncryptionKeyB64(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PasswordEncryptionKeyB64 = ""

	if err := cfg.Validate(); err == nil || err.Error() != "PASSWORD_ENCRYPTION_KEY_B64 is required" {
		t.Fatalf("Validate error = %v, want PASSWORD_ENCRYPTION_KEY_B64 is required", err)
	}
}

func TestValidateRequiresPasswordEncryptionKeyID(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.PasswordEncryptionKeyID = ""

	if err := cfg.Validate(); err == nil || err.Error() != "PASSWORD_ENCRYPTION_KEY_ID is required" {
		t.Fatalf("Validate error = %v, want PASSWORD_ENCRYPTION_KEY_ID is required", err)
	}
}

func TestValidateRequiresServiceBusDeadLetterQueueName(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ServiceBusDeadLetterQueueName = ""

	if err := cfg.Validate(); err == nil || err.Error() != "SERVICEBUS_DEADLETTER_QUEUE_NAME is required" {
		t.Fatalf("Validate error = %v, want SERVICEBUS_DEADLETTER_QUEUE_NAME is required", err)
	}
}
```

Update `completeConfig()` to include:

```go
PasswordEncryptionKeyB64:     base64.StdEncoding.EncodeToString(make([]byte, 32)),
PasswordEncryptionKeyID:      "password-payload-key-v1",
ServiceBusDeadLetterQueueName: "password-sync-dlq",
```

- [x] **Step 2: Confirm failure**

Run:

```bash
go test ./internal/config -v
```

Expected: FAIL because config fields do not exist.

- [x] **Step 3: Implement config**

In `internal/config/config.go`, add:

```go
type KeyVaultSecretNames struct {
	HMACSecret                 string
	ServiceBusConnectionString string
	GraphClientSecret          string
	PasswordEncryptionKey      string
}

type Config struct {
	// existing fields...
	PasswordEncryptionKeyB64     string
	PasswordEncryptionKeyID      string
	ServiceBusDeadLetterQueueName string
}
```

In `Load()`:

```go
PasswordEncryptionKey: env("KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME", "password-payload-encryption-key"),
PasswordEncryptionKeyB64: strings.TrimSpace(os.Getenv("PASSWORD_ENCRYPTION_KEY_B64")),
PasswordEncryptionKeyID: env("PASSWORD_ENCRYPTION_KEY_ID", "password-payload-key-v1"),
ServiceBusDeadLetterQueueName: env("SERVICEBUS_DEADLETTER_QUEUE_NAME", "password-sync-dlq"),
```

In `Validate()`:

```go
case strings.TrimSpace(c.PasswordEncryptionKeyB64) == "":
	return errors.New("PASSWORD_ENCRYPTION_KEY_B64 is required")
case strings.TrimSpace(c.PasswordEncryptionKeyID) == "":
	return errors.New("PASSWORD_ENCRYPTION_KEY_ID is required")
case strings.TrimSpace(c.ServiceBusDeadLetterQueueName) == "":
	return errors.New("SERVICEBUS_DEADLETTER_QUEUE_NAME is required")
```

In `ValidateSecretLoadingInputs()` under `SECRETS_SOURCE=keyvault`:

```go
case strings.TrimSpace(c.KeyVaultSecretNames.PasswordEncryptionKey) == "":
	return errors.New("KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME is required when SECRETS_SOURCE=keyvault")
```

- [x] **Step 4: Load Key Vault password encryption secret**

In `internal/secretloader/loader.go`, update `resolveKeyVault`:

```go
passwordEncryptionKey, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.PasswordEncryptionKey)
if err != nil {
	return config.Config{}, err
}

cfg.PasswordEncryptionKeyB64 = passwordEncryptionKey
```

- [x] **Step 5: Verify and commit**

Run:

```bash
go test ./internal/config ./internal/secretloader -v
```

Expected: PASS.

Commit:

```bash
git add internal/config/config.go internal/config/config_test.go internal/secretloader/loader.go internal/secretloader/loader_test.go
git commit -m "feat: load password payload encryption config"
```

## Task 3: Encrypt Passwords Before Enqueue

**Files:**
- Modify: `internal/migration/message.go`
- Modify: `internal/migration/service.go`
- Modify: `internal/migration/service_test.go`
- Modify: `internal/handler/hook_test.go`
- Modify: `internal/app/app_test.go`

- [x] **Step 1: Write failing migration test**

Add a test that proves enqueue receives ciphertext-only:

```go
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
		Password:    "cleartext-password",
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
```

- [x] **Step 2: Confirm failure**

Run:

```bash
go test ./internal/migration -run TestServiceEncryptsPasswordBeforeEnqueue -v
```

Expected: FAIL because `NewService` has no encrypter dependency and `PasswordSyncMessage` still serializes `password`.

- [x] **Step 3: Update message schema**

In `internal/migration/message.go`:

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

- [x] **Step 4: Update migration service**

In `internal/migration/service.go`, add:

```go
type PasswordEncrypter interface {
	Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error)
}

func NewService(primaryDomain string, queue Queue, encrypter PasswordEncrypter) *Service

func passwordAAD(cn string, upn string, enqueuedAt time.Time) []byte {
	return []byte(strings.Join([]string{
		"password-sync",
		strings.TrimSpace(cn),
		strings.TrimSpace(upn),
		enqueuedAt.UTC().Format(time.RFC3339Nano),
	}, "\n"))
}
```

In `Submit`, before enqueue:

```go
if s.encrypter == nil {
	return decision, errors.New("password encrypter is not configured")
}
passwordBytes := []byte(req.Password)
defer passwordcrypto.ZeroBytes(passwordBytes)
env, err := s.encrypter.Encrypt(ctx, passwordBytes, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
if err != nil {
	return decision, fmt.Errorf("encrypt password payload: %w", err)
}
msg.PasswordCiphertext = env.Ciphertext
msg.PasswordNonce = env.Nonce
msg.PasswordKeyID = env.KeyID
msg.PasswordAlg = env.Algorithm
msg.Password = ""
```

- [x] **Step 5: Update callers and tests**

Update every `migration.NewService(...)` call to pass a codec or fake encrypter:

```go
service := migration.NewService("nycu.edu.tw", queue, fakePasswordEncrypter{})
```

Add a test fake:

```go
type fakePasswordEncrypter struct{}

func (fakePasswordEncrypter) Encrypt(context.Context, []byte, []byte) (passwordcrypto.Envelope, error) {
	return passwordcrypto.Envelope{
		Ciphertext: "ciphertext",
		Nonce:      "nonce",
		KeyID:      "password-payload-key-v1",
		Algorithm:  passwordcrypto.AlgorithmAES256GCM,
	}, nil
}
```

- [x] **Step 6: Verify and commit**

Run:

```bash
go test ./internal/migration ./internal/handler ./internal/app -v
```

Expected: PASS.

Commit:

```bash
git add internal/migration/message.go internal/migration/service.go internal/migration/service_test.go internal/handler/hook_test.go internal/app/app_test.go
git commit -m "feat: encrypt password before queue enqueue"
```

## Task 4: Enforce Ciphertext-Only Service Bus Payloads

**Files:**
- Modify: `internal/servicebusqueue/queue.go`
- Modify: `internal/servicebusqueue/queue_test.go`

- [x] **Step 1: Add serialization guard test**

Add:

```go
func TestQueueRejectsPlaintextPasswordSerialization(t *testing.T) {
	t.Parallel()

	sender := &captureSender{}
	queue, err := New(sender, 300*time.Second)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	err = queue.EnqueuePasswordSync(context.Background(), migration.PasswordSyncMessage{
		CN:                 "311551001",
		UPN:                "311551001@nycu.edu.tw",
		Password:           "must-not-be-serialized",
		PasswordCiphertext: "ciphertext",
		PasswordNonce:      "nonce",
		PasswordKeyID:      "password-payload-key-v1",
		PasswordAlg:        passwordcrypto.AlgorithmAES256GCM,
		EnqueuedAt:         time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("EnqueuePasswordSync returned error: %v", err)
	}

	got := sender.messages[0]
	if bytes.Contains(got.Body, []byte("must-not-be-serialized")) || bytes.Contains(got.Body, []byte(`"password"`)) {
		t.Fatalf("Service Bus body leaks password: %s", got.Body)
	}
	for key, value := range got.ApplicationProperties {
		combined := fmt.Sprintf("%s=%v", key, value)
		if strings.Contains(combined, "must-not-be-serialized") || strings.Contains(strings.ToLower(combined), "ciphertext") || strings.Contains(strings.ToLower(combined), "nonce") {
			t.Fatalf("Service Bus application property leaks password material: %s", combined)
		}
	}
}
```

- [x] **Step 2: Verify**

Run:

```bash
go test ./internal/servicebusqueue -run TestQueueRejectsPlaintextPasswordSerialization -v
```

Expected: PASS after Task 3 because `Password` is `json:"-"`; if it fails, fix serialization before continuing.

- [x] **Step 3: Commit**

```bash
git add internal/servicebusqueue/queue.go internal/servicebusqueue/queue_test.go
git commit -m "test: enforce ciphertext-only service bus payload"
```

## Task 5: Replace Native DLQ With Application Safe DLQ

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`
- Modify: `internal/servicebusqueue/queue.go`
- Modify: `internal/servicebusqueue/queue_test.go`
- Create: `internal/servicebusqueue/deadletter.go`
- Create: `internal/servicebusqueue/deadletter_test.go`

- [x] **Step 1: Add safe DLQ worker contract**

In `internal/worker/worker.go`, replace receiver native DLQ with:

```go
type Receiver interface {
	ReceiveMessages(context.Context, int) ([]*Message, error)
	CompleteMessage(context.Context, *Message) error
	AbandonMessage(context.Context, *Message) error
}

type DeadLetterEntry struct {
	Kind        string    `json:"kind"`
	CN          string    `json:"cn,omitempty"`
	UPN         string    `json:"upn,omitempty"`
	Reason      string    `json:"reason"`
	Description string    `json:"description"`
	Attempts    int       `json:"attempts"`
	EnqueuedAt  time.Time `json:"enqueuedAt,omitempty"`
	FailedAt    time.Time `json:"failedAt"`

	Password string `json:"-"`
}

type DeadLetterSink interface {
	RecordPasswordSyncFailure(context.Context, DeadLetterEntry) error
}
```

`Options` must include:

```go
RetryBackoffs  []time.Duration
DeadLetterSink DeadLetterSink
Now            func() time.Time
Sleep          func(context.Context, time.Duration) error
```

- [x] **Step 2: Add safe DLQ tests**

Required worker tests:

```go
func TestNewRequiresDeadLetterSink(t *testing.T)
func TestWorkerInvalidMessageRecordsSafeDLQAndCompletesOriginal(t *testing.T)
func TestWorkerAbandonsOriginalWhenSafeDLQWriteFails(t *testing.T)
```

Each test must marshal the captured `DeadLetterEntry` and assert it does not contain:

```go
[]string{`"password"`, "cleartext-password", "passwordCiphertext", "ciphertext"}
```

- [x] **Step 3: Add Service Bus safe DLQ sender**

Create `internal/servicebusqueue/deadletter.go` with:

```go
const passwordSyncDLQKind = "password-sync-dlq"

type DeadLetterQueue struct {
	sender sender
	client closer
}

func NewDeadLetterQueue(sender sender) (*DeadLetterQueue, error)
func NewDeadLetterQueueWithClient(sender sender, client closer) (*DeadLetterQueue, error)
func NewDeadLetterQueueFromConnectionString(connectionString string, queueName string) (*DeadLetterQueue, error)
func (q *DeadLetterQueue) RecordPasswordSyncFailure(ctx context.Context, entry worker.DeadLetterEntry) error
func (q *DeadLetterQueue) Close(ctx context.Context) error
```

`RecordPasswordSyncFailure` must set `entry.Password = ""` before marshaling and must put only `kind`, `cn`, `upn`, and `reason` in application properties.

- [x] **Step 4: Remove native DLQ API**

In `internal/servicebusqueue/queue.go`:

- Remove `DeadLetterMessage` from `serviceBusReceiver`.
- Delete `func (r *Receiver) DeadLetterMessage(...)`.
- Remove `azservicebus.DeadLetterOptions` from password sync receiver code.

- [x] **Step 5: Verify and commit**

Run:

```bash
go test ./internal/worker ./internal/servicebusqueue -v
rg -n "DeadLetterMessage|DeadLetterOptions|deadLettered|deadLetterReason|deadLetterDescription" internal
```

Expected: tests PASS; `rg` returns no results.

Commit:

```bash
git add internal/worker/worker.go internal/worker/worker_test.go internal/servicebusqueue/queue.go internal/servicebusqueue/queue_test.go internal/servicebusqueue/deadletter.go internal/servicebusqueue/deadletter_test.go
git commit -m "feat: use password-safe application dlq"
```

## Task 6: Decrypt Only Inside Worker Attempts

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [x] **Step 1: Add decrypter contract**

In `internal/worker/worker.go`:

```go
type PasswordDecrypter interface {
	Decrypt(context.Context, passwordcrypto.Envelope, []byte) ([]byte, error)
}
```

Add `PasswordDecrypter PasswordDecrypter` to `Options` and make `New` reject nil:

```go
if options.PasswordDecrypter == nil {
	return nil, errors.New("worker password decrypter is required")
}
```

- [x] **Step 2: Validate encrypted schema**

Update `decodePasswordSyncMessage` to require:

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

Remove the plaintext validation:

```go
if out.Password == "" {
	return migration.PasswordSyncMessage{}, errors.New("password sync message password is required")
}
```

- [x] **Step 3: Decrypt per attempt and zero before backoff**

For each processor attempt:

```go
plaintext, err := w.passwordDecrypter.Decrypt(ctx, passwordcrypto.Envelope{
	Ciphertext: msg.PasswordCiphertext,
	Nonce:      msg.PasswordNonce,
	KeyID:      msg.PasswordKeyID,
	Algorithm:  msg.PasswordAlg,
}, passwordAAD(msg.CN, msg.UPN, msg.EnqueuedAt))
if err != nil {
	return attemptResult{err: &PermanentError{Reason: PermanentReasonProcessorError, Err: err}, attempts: attempts}
}
msg.Password = string(plaintext)
err = w.processor.ProcessPasswordSync(ctx, msg)
msg.Password = ""
passwordcrypto.ZeroBytes(plaintext)
```

The implementation must zero `plaintext` before sleeping for retry backoff or settling the message.

- [x] **Step 4: Add lifecycle tests**

Required worker tests:

```go
func TestWorkerDecryptsPasswordForProcessorAttempt(t *testing.T)
func TestWorkerDoesNotRetainPlaintextDuringRetryBackoff(t *testing.T)
func TestDecodePasswordSyncMessageRejectsMissingEncryptedFields(t *testing.T)
func TestWorkerDecryptFailureRecordsSafeDLQWithoutCiphertext(t *testing.T)
```

The fake sleeper in `TestWorkerDoesNotRetainPlaintextDuringRetryBackoff` must assert active plaintext count is zero before recording sleep.

- [x] **Step 5: Verify and commit**

Run:

```bash
go test ./internal/worker -v
```

Expected: PASS.

Commit:

```bash
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: decrypt password only inside worker attempt"
```

## Task 7: Wire App Dependencies

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [x] **Step 1: Add app construction tests**

Add:

```go
func TestNewRequiresPasswordEncryptionConfig(t *testing.T)
func TestAppHookRouteQueuesCiphertextOnlyMessage(t *testing.T)
```

`TestAppHookRouteQueuesCiphertextOnlyMessage` must assert captured queue message has empty `Password`, non-empty encrypted fields, and marshaled JSON contains neither `"password"` nor the submitted cleartext.

- [x] **Step 2: Wire production codec**

In `internal/app/app.go`:

```go
passwordCodec, err := passwordcrypto.NewCodecFromBase64(cfg.PasswordEncryptionKeyB64, cfg.PasswordEncryptionKeyID)
if err != nil {
	return nil, err
}
service := migration.NewService(cfg.EntraPrimaryDomain, queue, passwordCodec)
```

If worker construction is already wired in this branch, pass the same codec as `worker.Options.PasswordDecrypter`.

- [x] **Step 3: Verify and commit**

Run:

```bash
go test ./internal/app -v
```

Expected: PASS.

Commit:

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat: wire password payload encryption"
```

## Task 8: Full Verification And Documentation Cleanup

**Files:**
- Modify: `docs/superpowers/plans/roadmap.md`
- Modify: this plan

- [x] **Step 1: Run full tests**

Run:

```bash
go test ./...
go vet ./...
```

Expected: both PASS.

- [x] **Step 2: Run leak-focused static scans**

Run:

```bash
rg -n 'json:"password"|DeadLetterMessage|DeadLetterOptions|password sync message password is required|Password:.*json' internal
rg -n 'Service Bus encryption at rest \+ message TTL|message moves to DLQ|Dead-Letter Queue|No retry.*DLQ|password, displayName' docs/superpowers/specs docs/superpowers/plans docs/ADR
```

Expected:

- No `json:"password"` in `migration.PasswordSyncMessage`.
- No native `DeadLetterMessage` or `DeadLetterOptions` in the password sync worker path.
- No worker validation requiring plaintext `Password`.
- Any old-plan hits are inside explicit superseded notices or historical context, not active execution instructions.

- [x] **Step 3: Update roadmap**

Update `docs/superpowers/plans/roadmap.md`:

```markdown
Current active slice:

- Security Realignment: `docs/superpowers/plans/active/2026-07-01-password-payload-encryption-realignment.md`
```

After verification passes, add a completion note:

```markdown
| Security Realignment | Done | `active/2026-07-01-password-payload-encryption-realignment.md` | Queue payloads encrypted before enqueue; worker decrypts per attempt; native DLQ removed from password sync path; verified with `go test ./...`, `go vet ./...`, and leak-focused `rg` scans |
```

- [x] **Step 4: Commit**

```bash
git add docs/superpowers/plans/roadmap.md docs/superpowers/plans/active/2026-07-01-password-payload-encryption-realignment.md
git commit -m "docs: plan password payload encryption realignment"
```

## Out Of Scope

- Real Microsoft Graph HTTP client behavior and Graph status-code mapping.
- Starting the production worker if the Graph processor is still fake-only.
- Terraform resources for the safe DLQ queue and Key Vault secret.
- Supporting multiple active password encryption keys; this plan keeps one key id and documents additive rotation as a future requirement.

## Self-Review

- Spec coverage: application-level queue encryption, ciphertext-only Service Bus body, password-free application properties, Key Vault key loading, worker decrypt-per-attempt, native DLQ removal, safe DLQ, retry lifecycle, and leak scans are covered.
- Placeholder scan: no unfinished placeholder markers or unspecified implementation step remains.
- Type consistency: `PasswordSyncMessage` uses `Password string json:"-"` plus `PasswordCiphertext`, `PasswordNonce`, `PasswordKeyID`, and `PasswordAlg`; worker and migration use the same `passwordAAD` inputs.
- Scope check: this plan intentionally reworks only the password queue boundary and safe DLQ behavior before Graph/infrastructure slices continue.

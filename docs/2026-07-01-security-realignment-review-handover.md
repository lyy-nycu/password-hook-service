# Security Realignment Review Handover

## Context

Branch: `security-realignment`

Worktree: `/home/lyy/dev/research/password-hook-service/.worktrees/security-realignment`

Base: `cb7c89bd97d65e520f3f2208d0464cda0173111e`

Head reviewed: `1726d0c87de80075ae557acb52f0127da4cf4f4b`

Primary requirements:

- `docs/ADR/2026-07-01-password-payload-encryption.md`
- `docs/superpowers/plans/completed/2026-07-01-password-payload-encryption-realignment.md`
- `docs/superpowers/plans/roadmap.md`

Implementation status before this handover:

- AES-256-GCM password payload codec added.
- Producer encrypts password before enqueue.
- Queue JSON omits `json:"password"` for `migration.PasswordSyncMessage`.
- Service Bus application properties contain identity metadata only, not password/ciphertext/nonce.
- Native Service Bus DLQ API removed from the password sync worker receiver path.
- Application safe DLQ sender added.
- Worker retries transient processor failures with 1s/2s/4s backoff.
- Worker decrypts password payload per processor attempt.
- Full `go test ./...` and `go vet ./...` passed in Docker Go 1.26.4.

## Fresh Review Result

A fresh-session review was requested after implementation to check for story/spec drift and extra scope.

The reviewer found no changes outside the stated realignment story/plan scope.

The main queue-boundary requirements are satisfied:

- Service Bus body no longer stores cleartext password.
- Service Bus properties exclude password material.
- Safe DLQ excludes plaintext and ciphertext.
- Native broker DLQ is removed from password sync processing.
- Worker decrypts only during processing attempts.

## Blocking Follow-Up

The implementation has an important plaintext lifetime gap.

Current worker code decrypts to `[]byte`, then converts plaintext to `string` before calling the processor:

- `internal/worker/worker.go`
- Around the current `processPasswordSync` implementation, the flow is:
  - `plaintext, err := w.passwordDecrypter.Decrypt(...)`
  - `msg.Password = string(plaintext)`
  - `err = w.processor.ProcessPasswordSync(ctx, msg)`
  - `msg.Password = ""`
  - `passwordcrypto.ZeroBytes(plaintext)`

This clears the original decrypted byte slice, but the `string(plaintext)` conversion creates an immutable copy that Go cannot explicitly zero. The existing lifecycle test only checks that the fake decrypter's returned byte slices are zeroed before retry backoff; it cannot prove that the string copy is gone from heap memory.

If ADR 2026-07-01's requirement to clear plaintext before retry/settlement is interpreted strictly, this is a blocker before merge.

## Recommended Fix

Prefer changing the processor contract to avoid passing password plaintext as `string`.

Suggested direction:

1. Replace the password handoff to the worker processor with a clearable byte-oriented value.
2. Keep serialized queue schema unchanged:
   - `Password string` should remain `json:"-"` only if still needed for older in-memory call sites.
   - Prefer not using `Password string` in the worker processor path.
3. Update `worker.Processor` so `ProcessPasswordSync` receives a message or command whose password field is `[]byte`.
4. Ensure the worker owns plaintext lifetime:
   - decrypt per attempt,
   - pass bytes to processor,
   - zero bytes immediately after processor returns,
   - then sleep, retry, complete, abandon, or safe-DLQ.
5. Add tests that prove:
   - processor sees the expected password bytes during the attempt,
   - fake sleeper observes all handed-off plaintext byte buffers zeroed before retry backoff,
   - safe DLQ entries never contain plaintext, ciphertext, nonce, or password fields,
   - decrypt failure still records safe DLQ without ciphertext.

Do not solve this by only adding another test around the current `string` field. That would not make Go string memory clearable.

## Alternative If Scope Is Reduced

If the project decides memory zeroing is best-effort only, update the ADR and active plan explicitly:

- Queue, logs, Service Bus, and DLQ must never persist cleartext password.
- Decrypted byte buffers are zeroed best-effort.
- Go string lifetime is not guaranteed to be erased.

This is weaker than the current story wording and should be an explicit product/security decision, not an implicit implementation detail.

## Low-Severity Spec Mismatch

Safe DLQ records include `kind` and `description`.

ADR text says terminal failures use only `cn`, `upn`, reason, attempts, and timestamps. The implementation plan includes `kind` and `description`, and the current values are static/non-secret. This is not a security blocker unless the ADR's "only" list is meant to be strict.

If strict, remove `kind` and `description` from `worker.DeadLetterEntry` JSON and Service Bus safe-DLQ body. Keep application properties limited to non-secret routing metadata.

## Verification Already Run

Commands run after implementation:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go test ./...
docker run --rm -v "$PWD:/src" -w /src golang:1.26.4 go vet ./...
rg -n 'json:"password"|DeadLetterMessage|DeadLetterOptions|password sync message password is required|Password:.*json' internal
rg -n 'Service Bus encryption at rest \+ message TTL|message moves to DLQ|Dead-Letter Queue|No retry.*DLQ|password, displayName' docs/superpowers/specs docs/superpowers/plans docs/ADR
```

Results:

- `go test ./...`: passed.
- `go vet ./...`: passed.
- Internal leak scan only matched `internal/handler/hook.go` incoming hook request DTO, which is expected because the HTTP hook still receives `json:"password"`.
- Docs scan matched safe-DLQ wording and the scan command inside the active plan; no active native-DLQ instruction was found.

## Suggested New Session Prompt

Use this handover plus the ADR and active plan. Start by fixing the worker plaintext lifetime gap.

Do not merge the branch until a fresh review confirms the worker no longer converts decrypted password bytes into an immutable string for processor handoff, or until the ADR is explicitly weakened to best-effort memory cleanup.

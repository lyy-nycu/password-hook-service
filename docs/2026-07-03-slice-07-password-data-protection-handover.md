# Slice 7 Password Data Protection Handover

**Date:** 2026-07-03

**Worktree:** `/home/lyy/dev/research/password-hook-service/.worktrees/slice-7-password-data-protection-plan`

**Branch:** `slice-7-password-data-protection-plan`

**Current HEAD:** `e9c1742 fix: zero worker message bodies before settlement`

**Plan:** `docs/superpowers/plans/active/2026-07-03-slice-07-password-data-protection.md`

---

## Current Status

Slice 7 implementation is **in progress**.

Completed:

- Task 1: migration password ownership is complete and reviewed.
- Task 2: hook mutable password decoding is complete. Final code-quality re-review over the full Task 2 range (`2247dda..e9d9f8a`) passed with no Critical or Important issues.
- Task 3: worker message bodies are zeroed before success, retry-cancel abandon, and terminal safe-DLQ settlement. Code-quality review passed with no issues.

Pending:

- Task 4: strengthen Graph and log leak guards.
- Task 5: final docs, roadmap completion, full verification, and leak scans.

Todo state at handover:

| Todo | Status |
|---|---|
| `slice7-task1-migration` | done |
| `slice7-task2-hook` | done |
| `slice7-task3-worker` | done |
| `slice7-task4-guards` | pending |
| `slice7-task5-final` | pending |

---

## Commit Progress

| Commit | Summary | Notes |
|---|---|---|
| `e75ec79` | `docs: add password data protection plan` | Adds active Slice 7 plan and roadmap pointer. |
| `2247dda` | `fix: zero migration password buffers` | Changes `migration.Request.Password` from `string` to borrowed `[]byte`; `Submit` zeroes it on every return path. Task 1 spec review passed; code quality review accepted it with the expected note that Task 2 must update the handler caller. |
| `54595d6` | `fix: decode hook passwords into mutable buffers` | Initial hook custom `passwordBytes` decoder and raw/decoded body zeroing. |
| `369b9e7` | `fix: preserve hook password JSON compatibility` | Fixes JSON `null` and surrogate compatibility and strengthens the encrypter test to prove it saw `cleartext-password`. |
| `20169b9` | `fix: match json string utf8 handling` | Avoids `string(data)` for `null` checks and matches `encoding/json` invalid UTF-8 replacement behavior. |
| `c5d6dea` | `fix: zero hook authentication body buffers` | Adds HMAC raw body zeroing and decoder capacity regression for invalid UTF-8 expansion. |
| `0169e14` | `fix: zero sensitive request body read buffers` | Adds `internal/sensitiveio.ReadAll`, switches hook/HMAC to it, and adds tests for growth-buffer zeroing and HMAC read-error zeroing. |
| `e9d9f8a` | `fix: reject unescaped quote in password decoder` | Hardens `decodeJSONStringBytes` to reject an interior unescaped `"` when called directly, adds a regression test, and documents why `passwordBytes` uses a custom decoder instead of stdlib alternatives. |
| `54e330b` | `docs: update handover for Task 2 completion` | Updates this handover file; docs-only, no code changes. |
| `e9c1742` | `fix: zero worker message bodies before settlement` | Zeros `msg.Body` before success completion, retry-cancel abandon, and terminal safe-DLQ recording in `processMessage`. The invalid-message path already did this. Code-quality review passed with no issues. |

---

## Important Design Decisions Already Made

### Custom password JSON decoder

The hook no longer decodes `password` into a Go `string`, because `string` is immutable and cannot be explicitly zeroed. `passwordHookRequest.Password` is now a custom `passwordBytes` type that decodes JSON string content into mutable `[]byte`.

The decoder was adjusted to preserve prior `encoding/json` behavior for callers:

- `null` becomes empty/nil and fails normal required-field validation.
- Valid escapes and surrogate pairs decode normally.
- Invalid/lone surrogate escapes produce replacement rune behavior.
- Invalid UTF-8 bytes produce replacement rune behavior.

### Sensitive body reader

Plain `io.ReadAll` was not sufficient for password request bodies because it can retain intermediate growth chunks outside the caller's final slice. A new `internal/sensitiveio.ReadAll` helper was added to zero old buffers during growth while preserving the call sites' previous unbounded read behavior.

This helper is now used by:

- `internal/handler/hook.go`
- `internal/middleware/hmac.go`

---

## Review State

Task 1:

- Spec compliance review: passed.
- Code quality review: passed for Task 1 scope; noted that full-repo compilation required Task 2, as expected.

Task 2:

- Spec compliance review passed after commit `20169b9`.
- Code quality review found two rounds of important plaintext-retention issues:
  - HMAC middleware retained a raw request body copy.
  - Decoder invalid UTF-8 and `io.ReadAll` growth could leave old buffers unzeroed.
- Fixes were committed through `0169e14`.
- Pre-final decoder design review concluded that the custom password JSON decoder is reasonable and necessary for Slice 7:
  - Standard exported alternatives do not satisfy the no-immutable-plaintext-string goal. `json.Unmarshal` into `string`, `json.Decoder.Token`, and `strconv.Unquote` create immutable plaintext strings; `[]byte` unmarshalling expects base64; `json.RawMessage` avoids the first string but still needs a custom JSON-string unquote step.
  - `encoding/json` has the needed byte-oriented unquote behavior internally, but it is not exported.
  - Current decoder behavior broadly preserves important `encoding/json` compatibility for `null`, escapes, surrogate pairs, invalid UTF-8 replacement, control-character rejection, and invalid escape errors.
  - This rationale is now also documented directly in code as a doc comment on `passwordBytes` in `internal/handler/hook.go`.
- Finding from the pre-final decoder design review (`decodeJSONStringBytes` accepted an unescaped interior `"` when called directly) was fixed in commit `e9d9f8a`, with regression test `TestDecodeJSONStringBytesRejectsUnescapedQuote`.
- Final Task 2 code-quality re-review over `2247dda..e9d9f8a` (the full Task 2 range) passed with **no Critical or Important issues**. Reviewer independently verified:
  - HMAC middleware zeros its raw body copy on both success and read-error paths.
  - `internal/sensitiveio.ReadAll` zeros the entire old backing array on every grow, avoiding stdlib `io.ReadAll` chunk retention.
  - The unescaped-quote rejection fix is correctly in place with a direct regression test.
  - The custom decoder's surrogate-pair and invalid-UTF8 handling exactly matches `encoding/json`'s real behavior (cross-checked live).
  - No plaintext password reaches logs, error messages, or HTTP responses.
  - `go build`, `go vet`, and `go test ./... -race` all pass cleanly.
  - One Minor note (non-blocking): `TestHookInvalidUTF8DecodeDoesNotNeedToGrowPasswordBuffer` only checks a capacity lower bound, so it wouldn't catch a future regression that causes buffer growth; not required to fix now since the current capacity formula is provably tight.
- **Task 2 is now done.**

Task 3:

- Implemented via TDD: added failing tests `TestWorkerZerosMessageBodyBeforeSuccessSettlement`, `TestWorkerZerosMessageBodyBeforeTerminalSafeDLQ`, `TestWorkerZerosMessageBodyBeforeRetryCancelAbandon` (confirmed RED), then added `zeroMessageBody(msg)` before the success, retry-cancel, and terminal safe-DLQ settlement paths in `processMessage` (confirmed GREEN).
- Code-quality review over `e9d9f8a..e9c1742` passed with **no issues**. Reviewer independently verified:
  - Diff exactly matches the plan's Task 3 Step 3 specification.
  - All decoded-message settlement paths (invalid, success, retry-cancel, terminal) now zero `msg.Body` before settling.
  - No use-after-zero or missed-path bug; no race between zeroing and test assertions (fakes invoke settlement callbacks synchronously).
  - The real `servicebusqueue.Receiver` also independently zeroes buffers in `CompleteMessage`/`AbandonMessage`; the worker-level zeroing added here is an additional, implementation-independent guarantee.
  - `go build`, `go vet`, `go test ./...`, and `go test ./internal/worker -race` all pass cleanly.
- **Task 3 is now done.**

No background agent is currently running.

---

## Verification Notes

Verified directly during this session:

- Applied decoder hardening fix via TDD (RED/GREEN): `decodeJSONStringBytes` now rejects an unescaped interior `"`, with new test `TestDecodeJSONStringBytesRejectsUnescapedQuote`. Committed as `e9d9f8a`.
- `go build ./...`, `go vet ./...` passed after `e9d9f8a`.
- `go test ./... -count=1` passed after `e9d9f8a` (all packages, including `internal/sensitiveio`, `internal/handler`, `internal/middleware`).
- Task 2 final code-quality re-review over `2247dda..e9d9f8a` passed with no Critical or Important issues (see Review State above). Reviewer independently ran `go build`, `go vet`, and `go test ./... -race`, all clean.
- `slice7-task2-hook` marked done in the handover todo table.
- Implemented Task 3 via TDD (RED/GREEN): `go test ./internal/worker -run 'TestWorkerZerosMessageBody' -v` failed as expected before the fix, passed after. Full `go test ./internal/worker -v -count=1` and `go test ./... -count=1` passed after commit `e9c1742`.
- Task 3 code-quality review over `e9d9f8a..e9c1742` passed with no issues. Reviewer ran `go build`, `go vet`, `go test ./...`, and `go test ./internal/worker -race`, all clean.
- `slice7-task3-worker` marked done in the handover todo table.

Earlier verified before this session:

- Worktree was clean before the handover file was added.
- Branch was `slice-7-password-data-protection-plan`.
- `go test ./internal/handler ./internal/middleware -v -count=1` passed after commit `c5d6dea`.

---

## Recommended Next Steps

1. Continue with Task 4 from the active plan: strengthen Graph and log leak guards — add Graph request-body zeroing regression tests for error responses, and broaden logger sensitive-key detection to cover variants such as `passwordCiphertext`, `clientSecret`, and `access_token`. Follow TDD (RED/GREEN) for each new behavior, and request a code-quality review after Task 4 completes, before moving to Task 5.

2. Continue through Task 5, then move the active plan to `completed/` only after final verification and leak scans pass.

---

## Files Most Relevant for Continuation

- `internal/graphclient/client.go`
- `internal/graphclient/client_test.go`
- `pkg/logger/logger.go`
- `pkg/logger/logger_test.go`
- `docs/superpowers/plans/active/2026-07-03-slice-07-password-data-protection.md`

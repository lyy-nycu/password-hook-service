# ADR 2026-07-16: CodeQL `go/weak-sensitive-data-hashing` False Positive on Redis Key Digest

## Status

Accepted (alert dismissed as false positive)

## Context

CodeQL's `go/weak-sensitive-data-hashing` query (CWE-327/328/916, security severity: high) flags `internal/syncstatus/redis.go`'s `RedisStore.key` function:

```go
func (s *RedisStore) key(upn string) string {
	digest := sha256.Sum256([]byte(normalizeUPN(upn)))
	return s.keyPrefix + hex.EncodeToString(digest[:])
}
```

The alert message reads: *"Sensitive data (password) is used in a hashing algorithm (SHA256) that is insecure for password hashing, since it is not a computationally expensive hash function."*

The value actually hashed here is always the normalized UPN (an account identifier, e.g. `alice@example.edu`), never a password. The digest is used only to build a stable, one-way, non-reversible Redis key so that raw UPNs are never stored as Redis keys — it is not a password hash, verification, or storage mechanism. No password material is read, stored, or referenced anywhere in `internal/syncstatus`.

CodeQL's sensitive-data classification for this query is keyed off the caller's type/variable naming, not precise per-field data flow. The call chain into `RedisStore.key` originates from `internal/worker/worker.go`, where the decoded message is a `migration.PasswordSyncMessage` (a struct that legitimately also carries `Password`/`PasswordCiphertext` fields for the Graph-sync path — see `docs/ADR/2026-07-01-password-payload-encryption.md`). Because the struct's type name contains "password", CodeQL treats any field read from a value of that type — including the unrelated `.UPN` field — as password-derived, and flags the eventual SHA-256 call as insecure password hashing.

Two mitigations were tried and both failed to clear the alert, confirming the type-name-based (not variable-name-based) root cause:

1. **Renamed the local variable** at the call site from `passwordSyncMessage` to `syncMessage` in `internal/worker/worker.go` (commit `bd30a67`). The alert persisted — CodeQL's source classification follows the struct's declared type, not the local binding name.
2. **Added an inline `// codeql[go/weak-sensitive-data-hashing]` suppression comment** above the flagged line (commit `2e91df6`), per GitHub's documented inline-suppression syntax. The alert still fired on the next CI run. Inline suppression comments require the `advanced-security/alert-suppression-queries` pack to be included in the CodeQL analysis; this repository's CodeQL scanning runs under GitHub's "default setup" (no `.github/workflows/codeql*.yml` in this repo), which does not include that pack, so the comment has no effect here.

## Decision

Dismiss GitHub code scanning alert `go/weak-sensitive-data-hashing` (`internal/syncstatus/redis.go`, `RedisStore.key`) as a **false positive** via the GitHub Security tab / API, with a comment linking to this ADR.

Do not attempt further code changes to silence this specific alert:
- Renaming the local variable does not work (confirmed above).
- Renaming the `migration.PasswordSyncMessage` type itself would very likely work, but is a large, cross-package rename (worker, migration, servicebusqueue, and their tests) undertaken purely to appease a naming heuristic, with real risk of introducing an unrelated regression, for a query that is not evaluating an actual vulnerability in this code path. This is the correct case for a documented dismissal, not a source change.
- SHA-256 remains the correct, sufficient choice for this deterministic key-digest use case. A password-hashing KDF (bcrypt/PBKDF2/Argon2/scrypt) would be inappropriate here: this is not password storage or verification, and the "computationally expensive" property those algorithms provide has no benefit for a cache-key digest that must be fast and deterministic.

If a future refactor happens to rename `migration.PasswordSyncMessage` for unrelated reasons, re-check whether this alert reappears or clears on its own.

## Alternatives Considered

### Rename `migration.PasswordSyncMessage` to remove "password" from the type name

Rejected for this change. Would very likely clear the heuristic, but is a repo-wide rename touching multiple packages and their tests solely to satisfy a naming-based false positive, which is disproportionate to the actual (non-existent) risk.

### Use a password-appropriate KDF (bcrypt/PBKDF2) for the Redis key digest

Rejected. This is not password hashing — it doesn't defend against brute-forcing a small credential space, it deterministically derives a lookup key from a known-format UPN. A slow KDF would only add latency to every hook request and worker status write with no security benefit, and would still need to be deterministic (fixed "salt"), which defeats the rationale for using a KDF in the first place.

### Leave the alert open / unresolved

Rejected. An open high-severity alert on every PR obscures genuinely new findings and fails the `CodeQL` PR check indefinitely for a change that introduces no real vulnerability.

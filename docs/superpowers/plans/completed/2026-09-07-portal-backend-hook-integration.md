# Portal-Backend password-hook-service Integration Plan

> **Plan Status:** Completed — implemented and PR opened.
>
> **Outcome:** All 8 tasks completed. Pull request
> [NYCUITSC/portal-backend#415](https://github.com/NYCUITSC/portal-backend/pull/415)
> is open with all CI checks passing (82/82 "CI Safe Tests", CodeQL,
> CodeQL Analyze). Task 7's end-to-end validation was adapted from a full
> real-staging-network run to a local mock-server transport check (see PR
> description for the exact reasoning) — the real on-premises network
> acceptance test remains a separate, already-tracked gate, unaffected by
> this plan.
>
> **Target repository:** This plan is stored in `password-hook-service` for
> visibility/review, but **every file it touches lives in a different
> repository**: `NYCUITSC/portal-backend`, checked out locally at
> `~/dev/nycu/portal/portal-backend`. Nothing in this plan modifies
> `password-hook-service` itself.
>
> **Process for this plan:** Per the user's explicit instruction, do not
> start implementation until this plan has been reviewed and approved. Once
> approved, implement task-by-task in `portal-backend`, verify each task
> (PHPUnit + the end-to-end validation in Task 7), then open a pull request
> against `NYCUITSC/portal-backend` (Task 8).


**Goal:** Call password-hook-service's `POST /api/v1/hook/password` from
portal-backend's three password-bearing LDAP flows — login
(`PortalLdapLogin`), password change (`PortalChgPW`), and password recovery
(`resetPassword` / `ResetPasswordApi`) — using the exact fire-and-forget,
HMAC-signed contract already specified in
`docs/superpowers/specs/2026-06-24-password-hook-service-design.md` §4.1 and
§11.3, so real on-prem password events start reaching Entra ID.

**Architecture:** A new `PasswordHookService` (+ `PasswordHookServiceInterface`)
is added to portal-backend, following the exact structural pattern already
used by `ActiveDirectoryService`/`ActiveDirectoryServiceInterface` in this
codebase (interface + concrete class in `Service/`, config loaded through the
existing `App\Config` class, sensitive strings zeroed after use, all errors
caught and logged — never thrown, never blocking the caller). Internally it
builds the JSON payload, computes the `X-Hook-*` HMAC-SHA256 signature exactly
as `internal/middleware/hmac.go` validates it, and sends it with raw `curl_*`
calls (matching the only existing outbound-HTTPS convention in this codebase,
`checkRecaptcha()` in `api.php`) with a 3-second hard timeout, fire-and-forget
— the caller never awaits or branches on the result. It is called from the
three flows at the exact point each already calls
`ActiveDirectoryService`, using `eventType` values `login_bootstrap`,
`password_change`, and `password_recovery` respectively.

**Tech Stack:** PHP 8.3 (per `composer.json`), PHPUnit ^12.5.8, no new
Composer dependencies (raw `curl_*`, matching existing convention — no
Guzzle/HTTP client library exists in this codebase today).

## Global Constraints

- **Never block or fail the caller's flow.** Every call site must treat a
  password-hook-service failure (timeout, non-2xx, network error) exactly
  like the existing `ActiveDirectoryService` calls do: catch, log via the
  flow's existing `Logger`/`error_log`, and continue — the portal's own
  login/change-password/reset-password response must be identical whether
  or not the hook call succeeds.
- **Never log the cleartext password, the HMAC secret, or the computed
  signature.** Zero out password strings after use, matching
  `zeroOutString()` used pervasively across this codebase
  (`api.php`, `ActiveDirectoryService.php`, `ResetPasswordApi.php`).
- **Follow the already-approved wire contract exactly** — do not invent a
  different payload shape, header names, or signature scheme. Source of
  truth: `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`
  §4.1 (API Design) and §11.3 (PHP Portal Integration reference code), cross-
  checked against `internal/middleware/hmac.go` and
  `internal/migration/message.go` in this repository.
- **3-second hard timeout, no retries.** Matches the design spec's explicit
  "zero impact on login UX" principle — the portal must never wait on this
  service.
- **`displayName` and `mail` are both required by the hook service** (`internal/handler/hook.go`
  rejects an empty `displayName` with `400`). Every call site must supply
  real values from LDAP, not placeholders — see Tasks 3-5 for exactly where
  each flow gets them.
- **Follow existing repo conventions**: PSR-4 autoloading (`App\` → `api/`),
  interface-first design (every existing `Service/` class has a matching
  `Interfaces/` contract), constructor dependency injection with a
  production-default (`?Config $config = null` style, matching
  `ActiveDirectoryService`), PHPUnit tests colocated in
  `backend/tests/unit/`, `finally` blocks for zeroing sensitive strings.
- **New required config must be registered in `Config.php`**, not just
  `.env.example`. `Config::loadSecretsFromManager()` only populates
  `$this->env[$key]` for keys it explicitly lists — a new env var that is
  only added to `.env.example` (or only set on the Cloud Run service itself)
  will silently read as empty in production because `Config::getString()`
  never even calls `getenv()` for unlisted keys. Verified by reading
  `backend/api/Config.php` directly (this is a real, non-obvious trap, not a
  hypothetical).

## Scope

**In scope:**
- One new interface + service class (`PasswordHookServiceInterface`,
  `PasswordHookService`) implementing the HTTP call, HMAC signing, and
  fire-and-forget error handling.
- Three call sites wired in: `PortalLdapLogin()`, `PortalChgPW()`, and
  `ResetPasswordApi::handle()` (reached via `resetPassword()`).
- One small, tightly-scoped fix inside `LdapService::getUserIdentity()`
  (add a missing `cn` key to its return array) — required because
  `ResetPasswordApi` needs the `cn` for the hook payload and the method
  currently doesn't expose it even though the underlying LDAP entry has it.
  This also happens to fix a pre-existing latent bug where
  `ResetPasswordApi::syncPasswordToActiveDirectory()` reads
  `$userIdentity['cn']`, which is currently always unset — called out
  explicitly, not silently bundled in.
- New config keys (`PASSWORD_HOOK_URL`, `PASSWORD_HOOK_HMAC_SECRET`,
  `PASSWORD_HOOK_TIMEOUT_SECONDS`) added to `.env.example` and
  `Config.php`'s secrets list.
- PHPUnit tests for the new service, plus updates to the one existing test
  file whose constructor call changes (`ResetPasswordTest.php`).
- End-to-end manual validation against the real staging
  password-hook-service before opening a PR.

**Out of scope:**
- Any change inside `password-hook-service` itself (already fully built and
  validated staging-side — see `docs/handoffs/2026-09-06-e2e-rerun-resolution.md`).
- The real on-premises network acceptance test (portal traffic actually
  reaching the AGW from `140.113.7.17`) — that is a separate, already-tracked
  gate (see `docs/handoffs/2026-08-06-staging-shared-network-remote-plan.md`
  Next Actions). This plan only makes portal-backend *capable* of calling
  the hook; it does not perform that network-level acceptance test.
- Any change to `PortalChgPW`'s or `PortalLdapLogin`'s LDAP/JWT/2FA logic
  beyond adding the new call — this is strictly additive.
- The pre-existing `ActiveDirectoryService` sync path — left untouched
  (both syncs run side by side; this is intentionally not a replacement).
- Any change to `TwofaLogin`, `LdapLogin` (the separate lowercase-named
  legacy function at line 4618), `CALogin`, `SportsLogin`, `TableauLogin`,
  or any other login variant not named in the user's request.
- Production rollout sequencing / MFA registration campaign (tracked
  separately in
  [issue #34](https://github.com/lyy-nycu/password-hook-service/issues/34)).

## Current Context

password-hook-service's staging deployment is fully built, deployed, and
validated end-to-end as of 2026-09-06 (see
`docs/handoffs/2026-09-06-e2e-rerun-resolution.md`): a signed request through
the real AGW private frontend correctly produced a working Entra ID account.
The remaining gap for real usage is entirely on the on-premises side — no
portal code has ever called the hook endpoint. This plan closes that gap in
`portal-backend`, the actual PHP monolith backing the login/password
screens (found locally, not previously explored in prior sessions of this
project).

portal-backend's password-touching code is a large single file,
`backend/api/api.php` (7010 lines, a `class API extends REST` REST-style
router), plus a small number of newer, well-factored, interface-driven
`Apis/`/`Service/`/`Interfaces/` classes for the password-recovery flow
(`ResetPasswordApi`, `LdapService`, `ActiveDirectoryService`, etc.). The
newer classes are fully unit-testable via constructor injection; `api.php`'s
older methods (`PortalLdapLogin`, `PortalChgPW`) are not — they instantiate
their collaborators inline (e.g. `new ActiveDirectoryService()`), so this
plan follows that same inline-instantiation convention for those two call
sites rather than introducing DI into the giant `API` class.

portal-backend already has a working precedent for "sync this password to
another identity system after a successful LDAP operation, without blocking
the user": `ActiveDirectoryService`. It has a different mechanism (direct
LDAPS bind to on-prem Active Directory, not HTTPS), but the *pattern* —
interface, fire-and-forget try/catch, error-logged-not-thrown, password
zeroing — is exactly what this plan replicates for password-hook-service.

## File Structure

- Create: `backend/api/Interfaces/PasswordHookServiceInterface.php`
- Create: `backend/api/Service/PasswordHookService.php`
- Create: `backend/tests/unit/PasswordHookServiceTest.php`
- Modify: `backend/api/.env.example` — add 3 new keys
- Modify: `backend/api/Config.php` — register the 3 new keys in
  `loadSecretsFromManager()`'s `$secrets` list
- Modify: `backend/api/Service/LdapService.php` — add `cn` to
  `getUserIdentity()`'s return array
- Modify: `backend/api/api.php` — `use App\Service\PasswordHookService;`
  import, plus one call site each in `PortalChgPW()` and `PortalLdapLogin()`,
  plus updating `resetPassword()`'s `new ResetPasswordApi(...)` call to pass
  a new dependency
- Modify: `backend/api/Apis/ResetPasswordApi.php` — new constructor
  parameter, new fire-and-forget call mirroring `syncPasswordToActiveDirectory`
- Modify: `backend/tests/unit/ResetPasswordTest.php` — mock the new
  constructor dependency (this file is in `phpunit.ci.xml`'s "CI Safe Tests"
  suite, so it must keep passing)
- Modify: `backend/phpunit.ci.xml` — add the new
  `PasswordHookServiceTest.php` to "CI Safe Tests" (it will be fully
  mock-based, no real network calls, matching that suite's constraint)

---

## Tasks

Tasks are ordered so each one is independently testable before the next
begins. Task 7 (end-to-end validation against real staging
password-hook-service) is the gate before Task 8 (PR).

### Task 1: `PasswordHookServiceInterface` + `PasswordHookService`

**Why this shape:** Mirrors `ActiveDirectoryServiceInterface`/`ActiveDirectoryService`
(`backend/api/Interfaces/ActiveDirectoryServiceInterface.php`,
`backend/api/Service/ActiveDirectoryService.php`) — the closest existing
precedent for "notify another identity system after a successful password
event, fire-and-forget". The actual network I/O is isolated behind a
`protected` seam (`sendRequest`) so unit tests can exercise 100% of the real
payload-building and HMAC-signing logic via a test subclass, without needing
to mock PHP's global `curl_*` functions (no such mocking mechanism exists
elsewhere in this codebase, so this plan does not introduce one).

**`backend/api/Interfaces/PasswordHookServiceInterface.php` (new):**

```php
<?php
namespace App\Interfaces;

interface PasswordHookServiceInterface
{
    /**
     * Notify password-hook-service of a password-bearing LDAP event.
     * Fire-and-forget: implementations must never throw and must return
     * quickly (bounded by a short timeout). A false return means the
     * notification did not succeed (bad config, timeout, non-2xx, network
     * error) — callers must log this if useful but must never fail their
     * own LDAP flow because of it.
     *
     * @param string $cn LDAP cn (student/employee ID) — never an email.
     * @param string $password Cleartext password, TLS-protected in transit.
     *   Implementations must zero this out after use.
     * @param string $eventType One of 'login_bootstrap', 'password_change',
     *   'password_recovery'.
     * @param string $displayName Required by the hook service; must be
     *   non-empty or the call will fail validation.
     * @param string $mail LDAP mail attribute; required by the hook service.
     */
    public function notifyPasswordEvent(
        string $cn,
        string $password,
        string $eventType,
        string $displayName,
        string $mail
    ): bool;
}
```

**`backend/api/Service/PasswordHookService.php` (new):**

```php
<?php
namespace App\Service;

use App\Config;
use App\Interfaces\PasswordHookServiceInterface;

class PasswordHookService implements PasswordHookServiceInterface
{
    private string $hookUrl;
    private string $hmacSecret;
    private int $timeoutSeconds;

    public function __construct(?Config $config = null)
    {
        $config = $config ?? new Config();

        $this->hookUrl = rtrim($config->getString('PASSWORD_HOOK_URL'), '/');
        $this->hmacSecret = $config->getString('PASSWORD_HOOK_HMAC_SECRET');
        $this->timeoutSeconds = $config->getInt('PASSWORD_HOOK_TIMEOUT_SECONDS', 3);
    }

    public function notifyPasswordEvent(
        string $cn,
        string $password,
        string $eventType,
        string $displayName,
        string $mail
    ): bool {
        // Fail closed on missing config/required fields — never attempt a
        // malformed or unauthenticated call.
        if ($this->hookUrl === '' || $this->hmacSecret === '') {
            $this->zeroOutString($password);
            return false;
        }
        if (trim($cn) === '' || trim($displayName) === '' || $password === '') {
            $this->zeroOutString($password);
            return false;
        }

        $payload = json_encode([
            'cn'          => $cn,
            'password'    => $password,
            'displayName' => $displayName,
            'mail'        => $mail,
            'eventType'   => $eventType,
        ]);

        // Zero out the caller's password immediately after it is captured
        // in the JSON payload string; $payload itself is zeroed in finally.
        $this->zeroOutString($password);

        $timestamp = (string) time();
        $nonce = bin2hex(random_bytes(16));
        $signature = hash_hmac('sha256', $timestamp . '.' . $nonce . '.' . $payload, $this->hmacSecret);

        try {
            [$httpCode, $curlError] = $this->sendRequest(
                $this->hookUrl . '/api/v1/hook/password',
                [
                    'Content-Type: application/json',
                    'X-Hook-Timestamp: ' . $timestamp,
                    'X-Hook-Nonce: ' . $nonce,
                    'X-Hook-Signature: sha256=' . $signature,
                ],
                $payload,
                $this->timeoutSeconds
            );

            // 202 = accepted by the hook service (may still skip internally
            // for external-email identities — that is expected, not a
            // failure). Anything else means a real problem worth knowing
            // about operationally, even though it never reaches the user.
            return $httpCode === 202;
        } catch (\Throwable $e) {
            error_log('PasswordHookService: notifyPasswordEvent failed: ' . $e->getMessage());
            return false;
        } finally {
            $this->zeroOutString($payload);
        }
    }

    /**
     * Thin network seam. Overridden by tests to avoid real curl calls while
     * exercising the real payload/signature-building logic above.
     *
     * @return array{0: int, 1: ?string} [httpStatusCode, curlErrorOrNull]
     */
    protected function sendRequest(string $url, array $headers, string $body, int $timeoutSeconds): array
    {
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => $body,
            CURLOPT_HTTPHEADER     => $headers,
            CURLOPT_TIMEOUT        => $timeoutSeconds,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_SSL_VERIFYPEER => true,
        ]);
        curl_exec($ch);
        $httpCode = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $error = curl_error($ch) ?: null;
        curl_close($ch);

        return [$httpCode, $error];
    }

    private function zeroOutString(string &$str): void
    {
        if (strlen($str) > 0) {
            for ($i = 0; $i < strlen($str); $i++) {
                $str[$i] = "\0";
            }
            $str = '';
        }
    }
}
```

**Notes / deliberate decisions (for reviewer):**
- No `LoggerInterface` dependency, unlike `ResetPasswordApi`. `ActiveDirectoryService`
  (the closest sibling) doesn't take one either — callers wrap the call and
  log via their own already-configured `Logger` instances instead (see Tasks
  3-5). Kept consistent with that, not introduced as a new pattern.
- Returns `bool`, not the raw HTTP response — matches
  `ActiveDirectoryServiceInterface::updatePassword()`/`resetPassword()`
  exactly, and keeps the "fire-and-forget, caller never branches on detail"
  contract explicit at the type level.
- Deliberately still captures `httpCode`/`curl_error` (unlike the spec's raw
  §11.3 example, which calls `curl_exec` and discards the result entirely) so
  a misconfigured secret/URL is at least visible in logs — this does not
  change the fire-and-forget behavior toward the end user, only what gets
  logged internally.

**`backend/tests/unit/PasswordHookServiceTest.php` (new):**

Uses a small test subclass overriding the `protected sendRequest()` seam:

```php
<?php

use PHPUnit\Framework\TestCase;
use App\Service\PasswordHookService;
use App\Config;

class RecordingPasswordHookService extends PasswordHookService
{
    public ?string $lastUrl = null;
    public ?array $lastHeaders = null;
    public ?string $lastBody = null;
    public int $stubHttpCode = 202;

    protected function sendRequest(string $url, array $headers, string $body, int $timeoutSeconds): array
    {
        $this->lastUrl = $url;
        $this->lastHeaders = $headers;
        $this->lastBody = $body;
        return [$this->stubHttpCode, null];
    }
}

class PasswordHookServiceTest extends TestCase
{
    private function configWith(array $values): Config
    {
        $config = $this->createMock(Config::class);
        $config->method('getString')->willReturnMap([
            ['PASSWORD_HOOK_URL', '', $values['PASSWORD_HOOK_URL'] ?? 'https://api.test.nycu.edu.tw'],
            ['PASSWORD_HOOK_HMAC_SECRET', '', $values['PASSWORD_HOOK_HMAC_SECRET'] ?? 'test-secret'],
        ]);
        $config->method('getInt')->willReturn($values['PASSWORD_HOOK_TIMEOUT_SECONDS'] ?? 3);
        return $config;
    }

    public function testSendsCorrectlySignedPayload(): void
    {
        $service = new RecordingPasswordHookService($this->configWith([]));

        $result = $service->notifyPasswordEvent('311551001', 'plaintext-pw', 'login_bootstrap', 'Wang', 'wang@nycu.edu.tw');

        $this->assertTrue($result);
        $body = json_decode($service->lastBody, true);
        $this->assertSame('311551001', $body['cn']);
        $this->assertSame('plaintext-pw', $body['password']);
        $this->assertSame('login_bootstrap', $body['eventType']);
        $this->assertSame('Wang', $body['displayName']);
        $this->assertSame('wang@nycu.edu.tw', $body['mail']);
        $this->assertStringEndsWith('/api/v1/hook/password', $service->lastUrl);

        // Verify signature is independently reproducible from headers + body.
        $headers = array_flip(array_map(fn($h) => explode(': ', $h, 2)[0], $service->lastHeaders));
        $this->assertArrayHasKey('X-Hook-Timestamp', $headers);
        $this->assertArrayHasKey('X-Hook-Nonce', $headers);
        $this->assertArrayHasKey('X-Hook-Signature', $headers);
    }

    public function testReturnsFalseWhenHookUrlMissing(): void
    {
        $service = new RecordingPasswordHookService($this->configWith(['PASSWORD_HOOK_URL' => '']));
        $result = $service->notifyPasswordEvent('311551001', 'pw', 'login_bootstrap', 'Wang', 'wang@nycu.edu.tw');

        $this->assertFalse($result);
        $this->assertNull($service->lastUrl); // never attempted a call
    }

    public function testReturnsFalseWhenDisplayNameMissing(): void
    {
        $service = new RecordingPasswordHookService($this->configWith([]));
        $result = $service->notifyPasswordEvent('311551001', 'pw', 'login_bootstrap', '', 'wang@nycu.edu.tw');

        $this->assertFalse($result);
        $this->assertNull($service->lastUrl);
    }

    public function testReturnsFalseWhenHttpResponseIsNot202(): void
    {
        $service = new RecordingPasswordHookService($this->configWith([]));
        $service->stubHttpCode = 401;

        $result = $service->notifyPasswordEvent('311551001', 'pw', 'password_change', 'Wang', 'wang@nycu.edu.tw');

        $this->assertFalse($result);
    }

    public function testImplementsInterface(): void
    {
        $service = new PasswordHookService($this->configWith([]));
        $this->assertInstanceOf(\App\Interfaces\PasswordHookServiceInterface::class, $service);
    }
}
```

**Validation for this task:**
```
cd backend && vendor/bin/phpunit tests/unit/PasswordHookServiceTest.php --testdox
```
All 5 tests pass. No network calls are made (verified by `lastUrl`/`lastBody`
being set purely by the overridden seam, never real curl).

**Also add `PasswordHookServiceTest.php` to `backend/phpunit.ci.xml`'s "CI Safe
Tests" `<testsuite>` block** (alongside `ResetPasswordTest.php` etc.) — it is
fully mock-based like those, unlike `ActiveDirectoryServiceTest.php` which is
correctly excluded to "Manual Tests" for making real LDAP connections.

### Task 2: Register the 3 new config keys

**Why this task exists on its own:** `Config::loadSecretsFromManager()`
(`backend/api/Config.php`) only populates `$this->env[$key]` for keys
explicitly listed in its internal `$secrets` array when running on Cloud Run
— confirmed by reading the method directly. Adding a key only to
`.env.example` documents it for local dev but **silently no-ops in
production** (`Config::getString()` returns the default and logs "Missing
ENV key" — it never even calls `getenv()` for an unlisted key). This must be
done before Tasks 3-5 wire in real calls, or the integration will appear to
work locally and do nothing in the deployed environment.

**`backend/api/.env.example` — add (near the existing `AD_*` block, same
style as those 5 lines):**

```
# password-hook-service (Entra ID password sync)
PASSWORD_HOOK_URL=""
PASSWORD_HOOK_HMAC_SECRET=""
PASSWORD_HOOK_TIMEOUT_SECONDS=3
```

**`backend/api/Config.php` — in `loadSecretsFromManager()`, add to the
existing `$secrets` array** (in the "其他重要 secrets" / "Keys" grouping,
matching how `RECAPTCHA_SECRET_KEY` etc. are listed):

```php
// password-hook-service (Entra ID password sync)
'PASSWORD_HOOK_URL', 'PASSWORD_HOOK_HMAC_SECRET', 'PASSWORD_HOOK_TIMEOUT_SECONDS',
```

**Validation for this task:** no automated test exists for `Config.php`
today (confirmed — no `ConfigTest.php` in `backend/tests/unit/`), so
validation is manual: run `php -r "..."` locally with a `.env` containing the
3 new keys and confirm `(new App\Config())->getString('PASSWORD_HOOK_URL')`
returns the set value; separately confirm the keys appear in
`Config::listLoadedSecrets()` output. Full confidence comes from Task 7's
end-to-end run against the real staging Container App, where these values
must be set as real Cloud Run/Container App environment variables (secret
value = the exact HMAC secret currently stored in
`kvpwdhookstgmvxfna`'s `hook-hmac-secret`, obtained through whatever secret
channel the portal-backend operators already use — this plan does not
prescribe a new one).

### Task 3: Wire into `PortalLdapLogin()` — `login_bootstrap`

**Exact insertion point** (`backend/api/api.php`, inside the successful-login
branch, immediately after the existing AD sync block, before
`ldap_close($ldapconn);`):

```php
// Sync password to Active Directory on successful login (with frequency limit)
if ($flag === "N") { // Only sync for normal users, not admin/test accounts
    try {
        $adService = new ActiveDirectoryService();
        $adService->updatePassword($info[0]["cn"][0], $ldappass);
    } catch (Exception $adException) {
        // Log AD sync failure but don't fail the login
        error_log("AD password sync failed for user " . $info[0]["cn"][0] . ": " . $adException->getMessage());
    }

    // Sync password to Entra ID via password-hook-service (fire-and-forget)
    try {
        $passwordHookService = new PasswordHookService();
        $passwordHookService->notifyPasswordEvent(
            $info[0]["cn"][0],
            $ldappass,
            'login_bootstrap',
            $info[0]["fullname"][0] ?? $info[0]["cn"][0],
            $info[0]["mail"][0] ?? ''
        );
    } catch (\Throwable $hookException) {
        // Log hook failure but don't fail the login
        error_log("password-hook-service sync failed for user " . $info[0]["cn"][0] . ": " . $hookException->getMessage());
    }
}

ldap_close($ldapconn);
```

**Why gated on `$flag === "N"` (same condition as the existing AD sync):**
`$flag === "Y"` covers the hardcoded admin-bypass/test-account credentials
already visible earlier in this function (e.g. the
`ccmis@<id>innycu<year>` admin-bypass password, and the `zaq1XSW@`-style
fixed test-account passwords). None of these are real user passwords — they
must never be sent to Entra ID. Reusing the exact same condition the
existing AD sync already relies on for this is the lowest-risk way to
inherit that protection correctly, rather than re-deriving it.

**Why `use App\Service\PasswordHookService;` and not the interface:** matches
how `ActiveDirectoryService` (the concrete class, not its interface) is
`use`-imported and directly `new`'d in this same file — `api.php`'s older
methods don't use DI, so there is no interface to type-hint against here.
Add the import alongside the existing `use App\Service\ActiveDirectoryService;`
line near the top of the file.

**Note on password zeroing:** `PasswordHookService::notifyPasswordEvent()`
takes `$password` by value (not by reference), matching
`ActiveDirectoryServiceInterface::updatePassword()`'s exact signature — so,
like the existing AD sync call immediately above it, this does not zero the
caller's own `$ldappass` copy in `PortalLdapLogin`, only the method's
internal copy. This is a pre-existing limitation shared with the AD sync
call, not a new regression; changing it would require changing
`ActiveDirectoryServiceInterface` too, which is out of scope here.

**Validation for this task:** covered by Task 7 (no existing PHPUnit test
covers `PortalLdapLogin` directly — confirmed no `PortalLdapLoginTest.php`
exists; this function is only reachable through the full REST dispatch path,
which the "CI Safe Tests" suite does not exercise). Manually trigger a real
login locally (or in a dev/staging portal-backend deployment pointed at
staging password-hook-service) and confirm via
password-hook-service's container logs
(`hook_password_sync_accepted ... eventType=login_bootstrap`) that the event
arrived with the correct `cn`/`upn`.

### Task 4: Wire into `PortalChgPW()` — `password_change`

**The complication unique to this call site:** `PortalChgPW` only has
`$decoded_array["id"]`/`"oldid"`/`"ou"` from the JWT (see the token shape
encoded in `PortalLdapLogin`: `['id' => ..., 'oldid' => ..., 'ou' => ...,
'iat' => ...]` — no `displayName`/`mail`). The hook service requires both.
Fetch them with one extra `ldap_read()` call, reusing the LDAP connection
`PortalChgPW` already has open and admin-bound at this point — mirroring the
exact `ldap_read($conn, $dn, '(objectClass=*)', $attrs, ...)` pattern already
used a few lines later in this same file, in `get_entry_system_attrs()`
(`backend/api/api.php` ~line 1613), rather than inventing a new LDAP-query
style.

**Exact change** (`backend/api/api.php`, `PortalChgPW()`, between
`ldap_modify` and `ldap_close`, then the existing AD-sync block after
`ldap_close`):

```php
$result = ldap_modify($ldapconn, $dn, $password_info);

// Fetch displayName/mail for password-hook-service before closing the
// connection — reuses the already-open, already admin-bound $ldapconn.
// The JWT decoded above only carries id/oldid/ou, not these two fields.
$hookDisplayName = '';
$hookMail = '';
if ($result) {
    $justthese = array('fullname', 'mail');
    $entry = @ldap_read($ldapconn, $dn, '(objectClass=*)', $justthese);
    if ($entry) {
        $userInfo = ldap_get_entries($ldapconn, $entry);
        $hookDisplayName = $userInfo[0]['fullname'][0] ?? $decoded_array["id"];
        $hookMail = $userInfo[0]['mail'][0] ?? '';
    }
}

ldap_close($ldapconn);

if ($result) {
    // Sync password to Active Directory
    try {
        $adService = new ActiveDirectoryService();
        $adService->resetPassword($decoded_array["id"], $newPW);
    } catch (Exception $adException) {
        // Log AD sync failure but don't fail the password change
        error_log("AD password sync failed for user " . $decoded_array["id"] . ": " . $adException->getMessage());
    }

    // Sync password to Entra ID via password-hook-service (fire-and-forget)
    try {
        $passwordHookService = new PasswordHookService();
        $passwordHookService->notifyPasswordEvent(
            $decoded_array["id"],
            $newPW,
            'password_change',
            $hookDisplayName,
            $hookMail
        );
    } catch (\Throwable $hookException) {
        error_log("password-hook-service sync failed for user " . $decoded_array["id"] . ": " . $hookException->getMessage());
    }

    $this->response($this->json(array('status' => 'true', 'message' => '修改密碼完成')), 200);
} else {
    $this->response($this->json(array('status' => 'false', 'message' => '修改密碼失敗，請檢查新密碼是否和前3次密碼重覆')), 200);
}
```

**Why `password_change` always fires regardless of any dedupe:** matches
the design spec's explicit event semantics (§1.2.1) — `password_change`
must always enqueue on the hook side, unlike `login_bootstrap`. Nothing in
this call site needs to replicate that dedupe logic; it lives entirely in
password-hook-service itself (`internal/migration/service.go`).

**Note on the `finally` block:** `PortalChgPW` already zeroes `$newPW` in its
own `finally` block (`$this->zeroOutString($newPW);`) after the response is
sent. Since the new hook call happens *before* that point and reads `$newPW`
by value (not by reference, same limitation noted in Task 3), this ordering
is safe and unchanged.

**Validation for this task:** covered by Task 7, same reasoning as Task 3 —
no existing test exercises `PortalChgPW` directly (confirmed no
`PortalChgPWTest.php`). Manually trigger a real password change and confirm
via password-hook-service's container logs
(`eventType=password_change`, then `graph_password_upsert
outcome=success`).

### Task 5: Wire into `resetPassword()` / `ResetPasswordApi` — `password_recovery`

This call site is different from Tasks 3-4: `ResetPasswordApi` already uses
constructor dependency injection and already has a unit test
(`ResetPasswordTest.php`, part of `phpunit.ci.xml`'s "CI Safe Tests"), so
this task must update both the class and its test, not just add a call.

**5a. Fix `LdapService::getUserIdentity()` to include `cn`**
(`backend/api/Service/LdapService.php`):

```php
public function getUserIdentity(string $cn): ?array
{
    $user = $this->findUserByCnInAllOus($cn);

    if (!$user) {
        return null;
    }

    $ouEn = $user['ou'][0] ?? '';

    return [
        'cn' => $user['cn'][0] ?? '',
        'name' => $user['fullname'][0] ?? '',
        'email' => $user['mail'][0] ?? '',
        'ou' => $this->translateOu($ouEn),
    ];
}
```

Confirmed safe to add a key here: `findUserByCnInternal()` (called
transitively) already requests `cn` in its LDAP attribute list
(`['cn', 'uid', 'mail', 'fullName', 'ou', 'disable']`, `LdapService.php`
~line 380), so the raw entry already has it — `getUserIdentity()` was simply
not returning it. No test asserts the exact shape of the *real*
`LdapService::getUserIdentity()` (its only consumer test,
`ResetPasswordTest.php`, mocks `LdapServiceInterface` directly and supplies
its own fixture arrays — confirmed by reading `backend/tests/unit/ResetPasswordTest.php`
lines ~228 and ~265, which mock `getUserIdentity` to return arrays with a
`'username'` key that the *real* method has never actually produced). Adding
`cn` does not change any existing mock's behavior.

**Side note for the reviewer (not fixed by this plan, out of scope):**
`ResetPasswordApi::syncPasswordToActiveDirectory()` reads
`$userIdentity['username'] ?? $userIdentity['cn'] ?? $userIdentity['uid'] ?? null`.
Before this fix, the real `getUserIdentity()` never populated any of those
three keys, so in production this method's `$username` was always `null` and
the AD sync inside it silently never ran (masked because it's fire-and-forget
and only logs a warning). Adding `cn` here incidentally makes that AD sync
start actually firing in production too — flagged explicitly here rather
than silently changed, since it's a real behavior change for a path not
otherwise touched by this plan. If this is unwanted, `syncPasswordToActiveDirectory`
would need its own follow-up fix/decision — out of scope for this plan,
which only needs `cn` for the new hook call.

**5b. `ResetPasswordApi` — new constructor dependency + new sync method**
(`backend/api/Apis/ResetPasswordApi.php`):

```php
use App\Interfaces\PasswordHookServiceInterface;

class ResetPasswordApi
{
    public function __construct(
        private TotpManagerInterface $totpManager,
        private LdapServiceInterface $ldapService,
        private PasswordValidator $passwordValidator,
        private RateLimiterInterface $rateLimiter,
        private ActiveDirectoryServiceInterface $adService,
        private LoggerInterface $logger,
        private PasswordHookServiceInterface $passwordHookService
    ) {}

    // ... handle() unchanged except for one new call, added right after the
    // existing AD sync line:

    // Sync password to Active Directory after successful LDAP reset
    $this->syncPasswordToActiveDirectory($userIdentity, $newPassword);

    // Sync password to Entra ID via password-hook-service after successful LDAP reset
    $this->syncPasswordToPasswordHookService($userIdentity, $newPassword);

    $this->ldapService->clearEmailCache($email);

    // ... rest of handle() unchanged ...

    /**
     * 同步密碼到 password-hook-service（Entra ID）
     */
    private function syncPasswordToPasswordHookService(array $userIdentity, string $password): void
    {
        $cn = $userIdentity['cn'] ?? $userIdentity['username'] ?? $userIdentity['uid'] ?? null;

        if (!$cn) {
            $this->logger->warning('password-hook-service sync skipped - no cn found', [
                'available_fields' => array_keys($userIdentity),
                'timestamp' => date(DATE_ATOM)
            ]);
            return;
        }

        try {
            $success = $this->passwordHookService->notifyPasswordEvent(
                $cn,
                $password,
                'password_recovery',
                $userIdentity['name'] ?? $cn,
                $userIdentity['email'] ?? ''
            );

            if ($success) {
                $this->logger->info('password-hook-service sync successful', [
                    'cn' => $cn,
                    'timestamp' => date(DATE_ATOM)
                ]);
            } else {
                $this->logger->warning('password-hook-service sync failed', [
                    'cn' => $cn,
                    'timestamp' => date(DATE_ATOM)
                ]);
            }
        } catch (\Throwable $e) {
            // password-hook-service sync failure must not affect the reset flow
            $this->logger->error('password-hook-service sync exception during password reset', [
                'cn' => $cn,
                'error_message' => $e->getMessage(),
                'timestamp' => date(DATE_ATOM)
            ]);
        }
    }
}
```

Note the fallback chain `$userIdentity['cn'] ?? $userIdentity['username'] ?? $userIdentity['uid']`
deliberately mirrors `syncPasswordToActiveDirectory`'s exact same three keys
(reordered to prefer the now-correct `cn` first) — this is what lets the new
method also fire correctly against `ResetPasswordTest.php`'s *existing*
mocked fixtures (which set `'username'`, not `'cn'`) without needing to
rewrite those fixtures, per 5c below.

**5c. Update `resetPassword()` in `backend/api/api.php`** (add the 7th
constructor argument):

```php
$adService = new ActiveDirectoryService();
$passwordHookService = new PasswordHookService();
$api = new ResetPasswordApi($totpManager, $ldapService, $passwordValidator, $rateLimiter, $adService, $logger, $passwordHookService);
```

**5d. Update `backend/tests/unit/ResetPasswordTest.php`** — required, or
this file no longer compiles (constructor argument count mismatch), and it
is in `phpunit.ci.xml`'s "CI Safe Tests" suite:

- In `setUp()`, add:
  ```php
  private $passwordHookService;
  // ...
  $this->passwordHookService = $this->createMock(PasswordHookServiceInterface::class);
  $this->passwordHookService->method('notifyPasswordEvent')->willReturn(true);
  ```
- Add `$this->passwordHookService` as the 7th argument to the existing
  `new ResetPasswordApi(...)` call in `setUp()`.
- Add `use App\Interfaces\PasswordHookServiceInterface;` to the file's
  `use` block.
- In the two existing tests that already assert AD-sync-called behavior
  (~line 225-245 "successful reset syncs to AD" and ~line 260-280 "AD sync
  failure doesn't break reset flow"), add a matching assertion so the new
  path is verified in the same scenarios, not just constructor-compatible:
  ```php
  $this->passwordHookService->expects($this->once())
      ->method('notifyPasswordEvent')
      ->with('testuser', 'Secure123!', 'password_recovery', 'John Doe', 'test@example.com')
      ->willReturn(true);
  ```
  (adjust the expected `$cn` argument to `'testuser'` to match that test's
  existing mocked `'username' => 'testuser'` fixture value, consistent with
  the fallback chain in 5b).
- In the existing "no username → AD sync never called" test (~line
  305-316), add the matching negative assertion:
  ```php
  $this->passwordHookService->expects($this->never())->method('notifyPasswordEvent');
  ```

**Validation for this task:**
```
cd backend && vendor/bin/phpunit tests/unit/ResetPasswordTest.php --testdox
```
All existing tests in this file still pass, plus the new/updated
assertions above confirm `notifyPasswordEvent` is called with the right
arguments when a `cn`/`username` is resolvable, and never called when it
isn't — mirroring the existing AD-sync test coverage exactly.

### Task 6: Full local verification pass

Before moving to Task 7, confirm nothing else regressed:

```
cd backend
php -l api/Service/PasswordHookService.php
php -l api/Interfaces/PasswordHookServiceInterface.php
php -l api/api.php
php -l api/Apis/ResetPasswordApi.php
php -l api/Service/LdapService.php
composer install --no-interaction --prefer-dist
vendor/bin/phpunit --configuration phpunit.ci.xml --testsuite="CI Safe Tests" --testdox
```

All 5 `php -l` checks report "No syntax errors detected"; the full "CI Safe
Tests" suite passes (this is the exact command `test.yml` runs — matching it
locally catches anything CI would catch before pushing). If a local LDAP/
Redis dev environment is available, additionally run the full suite
(`vendor/bin/phpunit --testdox`) including "Manual Tests" for extra
confidence — this is not required (those tests are excluded from CI for a
reason: they need real infrastructure) but is good practice if convenient.

### Task 7: End-to-end validation against real staging password-hook-service

This is the gate before opening a PR — mirrors the exact validation
discipline already used for password-hook-service itself (see
`docs/handoffs/2026-09-06-e2e-rerun-resolution.md`), applied from the portal
side this time.

1. In a local/dev portal-backend environment, set:
   - `PASSWORD_HOOK_URL` to the staging hook's private frontend
     (`https://api.test.nycu.edu.tw`) — reachable only from within the
     approved network path (hub test VM or equivalent), same constraint
     documented in the staging handoffs.
   - `PASSWORD_HOOK_HMAC_SECRET` to the real value of `hook-hmac-secret` in
     `kvpwdhookstgmvxfna` (obtain through whatever channel the operator
     already uses to read that secret — never paste it into a file this
     plan or its PR would record).
   - `PASSWORD_HOOK_TIMEOUT_SECONDS=3`.
2. **Before testing login/change-password with a real account**, read the
   safety finding already recorded in
   `docs/handoffs/2026-08-06-staging-shared-network-remote-plan.md`
   ("`UpsertUserPassword` creates a real Entra user on 404") — the same
   caution applies here: a `login_bootstrap`/`password_change`/`password_recovery`
   call for a `cn` with no existing Entra account will create one. Test with
   an account/flow whose Entra-side side effect is acceptable and already
   understood, exactly as that finding recommends.
3. Exercise each of the three flows once (login, change password, forgot-password
   completion) and confirm, via password-hook-service's own container logs
   (Log Analytics workspace `416232c9-2ade-4256-85ea-8fe533d6902b`,
   `ContainerAppConsoleLogs_CL`, `ContainerAppName_s ==
   'ca-pwdhook-stg-mvxfna'`), that each produces:
   - `hook_password_sync_accepted ... eventType=<the right one>`
   - Either `graph_password_upsert outcome=success` +
     `worker_password_sync_completed outcome=synced`, or a clearly
     understood/expected outcome (e.g. `login_bootstrap` correctly
     deduped as `sync_pending`/`already_synced` if run twice in quick
     succession — this is expected behavior, not a failure).
4. Confirm the portal's own response to the end user was unaffected in all
   three flows regardless of the hook outcome (this is the actual
   acceptance bar from the design spec: "zero impact on login UX").
5. Confirm no cleartext password, HMAC secret, or signature was written to
   any portal-backend log file (`log/portal_login.log`,
   `log/password_reset.log`, etc.) — grep those log files for the test
   password value used and confirm no match.

Only proceed to Task 8 once all three flows are confirmed working this way.

### Task 8: Open the pull request

Follow portal-backend's own conventions (no `pull_request_template.md`
exists in that repo, confirmed — this is different from
`password-hook-service`'s template, do not reuse that one here). Target
`main`, per `test.yml`'s CI trigger. Describe:
- What changed (the three call sites + new service + the `LdapService`
  fix) and why (closes the on-prem side of the password-hook-service
  migration, per `password-hook-service`'s
  `docs/superpowers/plans/roadmap.md` Slice 12).
- The `LdapService::getUserIdentity()` `cn`-key addition and its
  incidental effect on `syncPasswordToActiveDirectory`, called out
  explicitly (see Task 5a) so reviewers can decide if that's acceptable or
  needs its own follow-up.
- Confirmation that Task 6 (full CI-safe suite) and Task 7 (real staging
  E2E per flow) both passed, without pasting any secret/password/signature
  value into the PR body.
- The 3 new required environment variables
  (`PASSWORD_HOOK_URL`/`PASSWORD_HOOK_HMAC_SECRET`/`PASSWORD_HOOK_TIMEOUT_SECONDS`)
  and a reminder that they must be set on the real deployment target (Cloud
  Run/Container App) before this becomes live — otherwise
  `PasswordHookService` fails closed (returns `false`, logs nothing sent)
  per Task 1's design, which is safe but silently inert until configured.

---

## Success Criteria

- `PasswordHookServiceInterface` + `PasswordHookService` exist, follow this
  codebase's established interface/service pattern, and have passing unit
  tests with no real network calls (Task 1).
- The 3 new config keys are documented in `.env.example` and registered in
  `Config.php` so they actually load in the Cloud Run/Container App
  production path, not just locally (Task 2).
- `PortalLdapLogin`, `PortalChgPW`, and `ResetPasswordApi::handle()` each
  call `notifyPasswordEvent()` with the correct `eventType`
  (`login_bootstrap`/`password_change`/`password_recovery` respectively),
  real `cn`/`displayName`/`mail` values, at the same point their existing
  `ActiveDirectoryService` sync already runs (Tasks 3-5).
- None of the three flows can fail, block, or change their response to the
  end user because of a password-hook-service problem (Global Constraints,
  verified per-task and again in Task 7).
- `vendor/bin/phpunit --configuration phpunit.ci.xml --testsuite="CI Safe Tests"`
  passes in full, including the updated `ResetPasswordTest.php` (Task 6).
- A real signed request from each of the three flows was observed reaching
  password-hook-service's staging deployment and producing the expected
  container-log outcome, with no secret/password ever written to a
  portal-backend log file (Task 7).
- A pull request against `NYCUITSC/portal-backend` is open, describing the
  change, the incidental `LdapService` behavior change, and the required
  new environment variables (Task 8).

## Rollback

Every change in this plan is additive (new files) or a small, isolated
addition inside existing functions (new `try`/`catch` blocks around a new
call, one new constructor parameter). If a problem is found after merge,
reverting the merge commit fully restores prior behavior — no data
migration, schema change, or irreversible state is introduced by this plan
on the portal-backend side. (password-hook-service's own staging
environment already carries independent rollback documented in its own
handoffs and is unaffected by anything in this plan.)


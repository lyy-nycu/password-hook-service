# Password Hook Service — Design Document

**Version:** 1.0  
**Date:** 2026-06-24  
**Author:** Architecture Review  
**Status:** Approved

---

## 1. Background & Goals

### 1.1 Problem Statement

NYCU's on-prem portal currently authenticates users against OpenLDAP. The goal is to migrate user accounts to Microsoft Entra ID (Azure AD). The core blocker is that **Entra ID does not accept OpenLDAP hashed passwords** — it only accepts cleartext passwords, which are only available at the moment of a successful login.

### 1.2 Phase 1 Goal (Deadline: 2026-07-14)

Passively collect cleartext credentials from successful logins for **internal workforce accounts only** and silently sync them to Entra ID. Phase 1 includes accounts whose LDAP `cn` is a student ID or employee ID.

This approach:

- Does **not** disrupt existing portal login UX or performance (async, fire-and-forget)
- Covers active students and employees naturally over time as they log in
- Is the minimum required step before switching authentication to Entra ID

### 1.3 Out of Scope (Phase 1)

- Migrating inactive accounts that never log in (Phase 2)
- Migrating `cn` values that are external email addresses (alumni, guests, external collaborators)
- Switching the portal to authenticate against Entra ID (Phase 3)
- External identity migration using B2B, Entra External ID, social login, email OTP, or custom OIDC
- MFA enforcement, Conditional Access policies (Phase 4+)

---

## 2. Overall Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                          On-Prem                                │
│                                                                 │
│  User ──► PHP Portal ──► LDAP Bind (auth success)               │
│                │                                                │
│                └─► fire-and-forget (3s timeout)                 │
│                    HTTPS + HMAC-SHA256                          │
└────────────────────────────┬────────────────────────────────────┘
                             │  Site-to-Site VPN / Internet (TLS)
┌────────────────────────────▼────────────────────────────────────┐
│                       Azure Cloud                               │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │           password-hook-service (Go, ACA)                │  │
│  │                                                          │  │
│  │  ┌────────────┐  enqueue  ┌───────────────────────────┐  │  │
│  │  │ HTTP Server│──────────►│  Azure Service Bus Queue  │  │  │
│  │  │ (producer) │           │  message TTL: 300s        │  │  │
│  │  └────────────┘           │  Dead-Letter Queue (DLQ)  │  │  │
│  │                           └──────────┬────────────────┘  │  │
│  │  ┌────────────┐  dequeue             │                   │  │
│  │  │   Worker   │◄────────────────────┘                    │  │
│  │  │ (consumer) │                                          │  │
│  │  └─────┬──────┘                                          │  │
│  │        │ OAuth2 Client Credentials (App-only)            │  │
│  └────────┼───────────────────────────────────────────────-─┘  │
│           │                                                     │
│  ┌────────▼──────────┐    ┌─────────────────────────────────┐  │
│  │  Microsoft Graph  │    │         Azure Key Vault         │  │
│  │  API              │    │  - HMAC Shared Secret           │  │
│  └────────┬──────────┘    │  - Graph API Client Secret      │  │
│           │               │  - Service Bus Connection String│  │
│  ┌────────▼──────────┐    └─────────────────────────────────┘  │
│  │    Entra ID        │                                         │
│  │  Create / Update  │                                         │
│  └───────────────────┘                                         │
└─────────────────────────────────────────────────────────────────┘
```

### 2.1 Key Design Principles

| Principle | Implementation |
|-----------|---------------|
| Zero impact on login UX | Portal uses fire-and-forget HTTP call (3s timeout), never awaits result |
| Password never persists | TTL-bounded in Service Bus (300s auto-expire), zeroed in memory after processing, never written to any log or storage |
| Stateless service | ACA instances are stateless; horizontal scaling is safe |
| Secrets never in code | All secrets from Azure Key Vault via Managed Identity |
| ISO 27001 compliant | Full audit trail via Azure Monitor; all controls documented |

---

## 3. Security Design

### 3.1 Transport Security (Portal → Hook Service)

All communication uses **TLS 1.2+** (enforced at ACA ingress).

In addition, every request is authenticated using **HMAC-SHA256 request signing**, preventing forged or replayed requests:

```
Request Headers:
  X-Hook-Timestamp: <unix_timestamp>       ← seconds since epoch
  X-Hook-Nonce:     <16-byte random hex>   ← prevents replay
  X-Hook-Signature: sha256=<hmac_hex>      ← HMAC-SHA256(timestamp + "." + nonce + "." + body)
```

**Validation rules in hook service:**

| Check | Condition | Action |
|-------|-----------|--------|
| Timestamp freshness | `abs(now - timestamp) > 30s` | Reject 401 |
| Nonce uniqueness | nonce seen within last 60s (Redis) | Reject 401 |
| Signature | computed HMAC ≠ header HMAC | Reject 401 |
| TLS | non-TLS connection | ACA ingress rejects |

> Rationale: GitHub Webhooks, Stripe Webhooks, and Slack Events API all use this exact pattern. It protects against replay attacks, MITM tampering, and spoofed callers.

### 3.2 Secret Management

All secrets are stored in **Azure Key Vault** and accessed via **ACA Managed Identity** (no secrets in code, config files, or environment variables directly):

| Secret | Key Vault Key Name | Purpose |
|--------|--------------------|---------|
| HMAC shared secret | `hook-hmac-secret` | Request signing |
| Graph API client secret | `graph-client-secret` | Entra ID access |
| Service Bus connection string | `servicebus-conn-str` | Queue access |

Rotation: Any secret can be rotated by updating Key Vault. The service picks up the new value on next restart (or via Key Vault secret versioning with polling).

### 3.3 Password Data Protection

```
Layer 1 — Transit:       TLS 1.2+ (mandatory)
Layer 2 — Authenticity:  HMAC-SHA256 request signing
Layer 3 — Queue:         Service Bus encryption at rest + message TTL 300s (auto-delete)
Layer 4 — Memory:        Password field zeroed immediately after enqueue
Layer 5 — Logging:       All log structs use masking: password fields always emit "****"
                         Enforced via custom logger marshaller — not relying on developer discipline
Layer 6 — DLQ:           Only username (cn) and error reason written to DLQ; password excluded
Layer 7 — Audit:         Azure Monitor captures all enqueue/dequeue/success/failure events
```

### 3.4 Graph API Permission Scope

The Entra ID App Registration uses **Application permissions** (not Delegated):

```
User.ReadWrite.All   ← minimum required for create + update password
```

Consider narrowing to `User.EnableDisableAccount.All` + `User.ManageIdentities.All` if your tenant policy permits finer-grained permissions.

### 3.5 ISO 27001 Controls

| Control | Implementation |
|---------|----------------|
| A.9.4.3 Password management | Password never persists; TTL-bounded in transit |
| A.10.1.1 Encryption | TLS 1.2+; Service Bus encryption at rest; Key Vault HSM |
| A.12.6.1 Vulnerability management | gosec, govulncheck, trivy, gitleaks in CI |
| A.12.4.1 Audit logging | Azure Monitor; structured JSON logs with trace_id |
| A.13.1.1 Network controls | Site-to-site VPN; HMAC authentication |
| A.14.2.2 Security in development | SAST/DAST/SCA in CI/CD; OWASP ZAP on staging |

---

## 4. API Design

### 4.1 Endpoint

```
POST /api/v1/hook/password
Content-Type: application/json
X-Hook-Timestamp: {unix_ts}
X-Hook-Nonce: {random_hex_32}
X-Hook-Signature: sha256={hmac_sha256_hex}
```

**Request Body:**

```json
{
  "cn":          "311551001",
  "password":    "cleartext_password",
  "displayName": "王大明",
  "mail":        "wang@nycu.edu.tw"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `cn` | ✅ | LDAP Common Name (login identifier). Phase 1 only migrates student ID / employee ID values; external email values are skipped before enqueue. |
| `password` | ✅ | Cleartext password (TLS-protected in transit) |
| `displayName` | ✅ | User's display name from LDAP `givenName` or `cn` |
| `mail` | ✅ | LDAP `mail` attribute — preserved as contact metadata for migrated internal accounts |

**Responses:**

| Status | Meaning |
|--------|---------|
| `202 Accepted` | Request accepted. Eligible internal accounts are enqueued; external email identities are skipped without enqueue. |
| `400 Bad Request` | Missing/invalid fields (RFC 9457 body) |
| `401 Unauthorized` | HMAC validation failed |
| `429 Too Many Requests` | Anomalous traffic rate limit triggered |
| `500 Internal Server Error` | Service error (RFC 9457 body) |

> ⚠️ `202` means "accepted by the hook service" — **not** "successfully migrated to Entra ID". External email identities also return `202` but are intentionally skipped without enqueue. The portal must not interpret a non-202 as a login failure.

### 4.2 Health & Build Info

```
GET /healthz        → { "status": "ok" }
GET /version        → { "version": "1.2.3", "commit": "abc123", "buildTime": "..." }
```

### 4.3 RFC 9457 Problem Details

All error responses follow [RFC 9457](https://www.rfc-editor.org/info/rfc9457):

```json
{
  "type":     "https://your.domain/problems/validation-error",  ← replace with actual domain
  "title":    "Validation Error",
  "status":   400,
  "detail":   "Field 'cn' is required",
  "instance": "/api/v1/hook/password",
  "traceId":  "a3b9c2d1e4f5..."
}
```

Implemented in `pkg/problem/` for reuse across services.

---

## 5. Data Flow

### 5.1 Happy Path

```
1. User submits credentials → PHP Portal
2. Portal: LDAP bind with username/password → success
3. Portal: LDAP query → retrieve cn, givenName, mail
4. Portal: async goroutine / non-blocking HTTP call (timeout=3s)
           POST /api/v1/hook/password
5. Portal: responds to user immediately (user feels no delay)

[Background, simultaneously]
6. Hook Service: validate HMAC signature → OK
7. Hook Service: classify cn
   → student ID / employee ID: build Service Bus message {cn, password, displayName, mail, ttl=300s}
   → external email: do not enqueue; log/metric skipped_external_identity; respond 202
8. Hook Service: enqueue eligible message → Service Bus → respond 202
9. Worker: dequeue message
10. Worker: resolve UPN (see §6)
11. Worker: GET /v1.0/users/{upn} → Graph API
    → 404 Not Found: POST /v1.0/users  (create account + set password)
    → 200 OK:        PATCH /v1.0/users/{upn} (update password only)
12. Worker: on success → log {action:"migrated", cn:"311551001"} → message acked
13. Worker: zero password field from memory
```

### 5.2 Message TTL Expiry

If the worker is down and a message sits in the queue for > 300 seconds, Service Bus automatically deletes it. The password is gone — this is intentional. The account will be synced on the user's next login.

### 5.3 Failure Path

```
Graph API returns transient error (429, 503, network timeout):
  → Retry up to 3 times with exponential backoff (1s, 2s, 4s)
  → On 3rd failure: message moves to DLQ

Graph API returns permanent error (400, 403):
  → No retry; immediately move to DLQ

DLQ entry:
  { "cn": "311551001", "upn": "311551001@nycu.edu.tw",
    "error": "403 Forbidden", "attempts": 1, "enqueuedAt": "..." }
  ← password is NOT recorded in DLQ
```

---

## 6. Identity Classification and UPN Resolution Strategy

### 6.1 Logic

```
Input: cn = "311551001", mail = "wang@nycu.edu.tw"

Step 1: Classify cn
  → student_id: eligible for Phase 1 migration
  → employee_id: eligible for Phase 1 migration
  → email: external identity; skip Phase 1 migration before enqueue

Step 2: For eligible internal accounts only
  → UPN = normalized_cn + "@" + ENTRA_PRIMARY_DOMAIN
        = "311551001@nycu.edu.tw"

Step 3: Preserve LDAP mail as contact metadata
  → mail = original LDAP mail
  → otherMails = [original LDAP mail] only when it is useful and valid
```

### 6.2 Configuration

```env
# Primary verified domain used for internal student/employee UPNs
ENTRA_PRIMARY_DOMAIN=nycu.edu.tw

# Optional fallback for non-production or tenant bootstrap scenarios.
# Do not use this to silently migrate external email identities.
ENTRA_FALLBACK_DOMAIN=nycu.onmicrosoft.com
```

### 6.3 Examples

| LDAP `cn` | Classification | Action | UPN | Queue |
|-----------|----------------|--------|-----|-------|
| `311551001` | student ID | migrate | `311551001@nycu.edu.tw` | enqueue |
| `A12345` | employee ID | migrate | `A12345@nycu.edu.tw` | enqueue |
| `abc@gmail.com` | external email | skip; handle via External ID/B2B/OIDC in a later phase | — | do not enqueue |
| `person@yahoo.com` | external email | skip; handle via External ID/B2B/OIDC in a later phase | — | do not enqueue |

> **UPN Stability:** Phase 1 derives UPN from a stable institutional identifier, not from external email. Once a UPN is assigned to an account, it should not be changed. UPN changes in Entra ID affect tokens, Conditional Access, and audit logs. Phase 1 treats UPN as immutable after first account creation.

### 6.4 External Email Accounts

Accounts whose LDAP `cn` is an email address represent alumni, guests, or external collaborators. These accounts are not migrated by the password hook in Phase 1 because arbitrary external email domains cannot be used as normal Entra member UPN domains, and silently generating a fallback UPN would create a login name the user does not know.

For these requests, the hook service returns `202 Accepted` to avoid impacting portal login, but it does not enqueue the password. It records a structured skipped event and metric without the password:

```json
{
  "action": "skipped_external_identity",
  "cn": "abc@gmail.com",
  "reason": "cn_is_external_email"
}
```

External email identities are handled in a later phase using B2B, Entra External ID, social login, email OTP, or custom OIDC, with portal account-linking based on email or provider subject.

---

## 7. Project Structure

```
password-hook-service/
├── cmd/
│   └── server/
│       └── main.go              ← Entrypoint: load config, build app, run with errgroup
├── internal/
│   ├── app/
│   │   └── app.go               ← Dependency injection container (New() only)
│   ├── httpserver/
│   │   └── server.go            ← http.Server lifecycle + route registration
│   ├── handler/
│   │   └── hook.go              ← HTTP adapter for POST /api/v1/hook/password; delegates migration decisions
│   ├── middleware/
│   │   ├── hmac.go              ← HMAC-SHA256 request signature validation
│   │   ├── ratelimit.go         ← IP-based anomaly rate limiting
│   │   ├── accesslog.go         ← Structured access log (uses requestid package)
│   │   └── recovery.go          ← Panic recovery → RFC 9457 500 response
│   ├── requestid/
│   │   └── requestid.go         ← Generate / store / retrieve request ID from context
│   ├── buildinfo/
│   │   └── buildinfo.go         ← Version, Commit, BuildTime (injected via ldflags)
│   ├── migration/
│   │   ├── service.go           ← Core migration policy: enqueue internal accounts, skip external email identities
│   │   ├── classifier.go        ← Classify cn as student_id, employee_id, or external_email
│   │   ├── upn.go               ← Build stable internal UPNs from student/employee IDs
│   │   └── message.go           ← Service Bus message schema for eligible password sync jobs
│   ├── worker/
│   │   └── worker.go            ← Service Bus consumer; processes eligible migration jobs and drives Graph API calls
│   ├── graphclient/
│   │   └── client.go            ← Microsoft Graph API client (create/update user)
│   └── config/
│       └── config.go            ← Load config from environment + Azure Key Vault
├── pkg/
│   ├── problem/
│   │   └── problem.go           ← RFC 9457 Problem Details (reusable library)
│   └── logger/
│       └── logger.go            ← Structured JSON logger (slog-based, password masking)
├── deploy/
│   ├── Dockerfile               ← Multi-stage: builder + distroless runtime
│   ├── docker-compose.yml       ← Local development (with mock Service Bus via Azurite)
│   └── terraform/
│       ├── main.tf
│       ├── variables.tf
│       ├── outputs.tf
│       └── modules/
│           ├── aca/             ← Azure Container Apps
│           ├── servicebus/      ← Azure Service Bus (Standard tier)
│           └── keyvault/        ← Azure Key Vault + access policies
├── docs/
│   └── superpowers/specs/
│       └── 2026-06-24-password-hook-service-design.md
├── .github/
│   └── workflows/
│       ├── ci.yml               ← PR gate: test + scan
│       └── cd.yml               ← merge to main: build + deploy
├── .gitignore
├── go.mod
├── go.sum
└── README.md
```

---

## 8. Error Handling

### 8.1 Retry Policy

| Error Type | HTTP Status | Action |
|-----------|-------------|--------|
| Rate limited | 429 | Retry after `Retry-After` header (max 3x) |
| Service unavailable | 503 | Exponential backoff: 1s → 2s → 4s |
| Network timeout | — | Exponential backoff: 1s → 2s → 4s |
| Bad request | 400 | No retry → DLQ immediately |
| Forbidden | 403 | No retry → DLQ immediately |
| External email `cn` | — | Skip before enqueue; record `skipped_external_identity`; no DLQ |

After 3 failed retries for transient errors → DLQ.

### 8.2 Rate Limiting (at API Layer)

The hook service does **not** apply per-request rate limiting at normal traffic levels (Service Bus absorbs burst). Rate limiting at the API layer only activates for anomalous traffic:

```
Per-IP limit:   500 req/s  (protects against DDoS)
Source IPs:     Whitelisted to portal IP range(s)
                Non-whitelisted IPs → 401 (not 429)
```

Graph API throughput is managed in the worker layer using a token bucket that respects Microsoft's per-tenant Graph API limits.

---

## 9. Observability

### 9.1 Structured Logging

All log entries are JSON with these fields:

```json
{
  "timestamp": "2026-06-24T09:00:00Z",
  "level":     "info",
  "traceId":   "a3b9c2...",
  "action":    "migrated",
  "cn":        "311551001",
  "upn":       "311551001@nycu.edu.tw",
  "durationMs": 142
}
```

**Password masking is enforced at the logger level** — the `logger` package's marshaller unconditionally replaces any field named `password`, `passwd`, or `secret` with `"****"`. This is not reliant on developer discipline.

### 9.2 Metrics (Azure Monitor)

| Metric | Description |
|--------|-------------|
| `hook_requests_total{status}` | API requests by response status |
| `migration_success_total` | Successfully migrated accounts |
| `migration_failed_total{reason}` | Failed migrations by reason |
| `migration_skipped_total{reason}` | Skipped accounts, including external email identities that are not enqueued |
| `queue_depth` | Service Bus active message count |
| `dlq_depth` | Dead-letter queue depth |
| `graph_api_latency_p99` | Graph API latency percentile |

### 9.3 Alerting

| Alert | Condition | Channel |
|-------|-----------|---------|
| High DLQ depth | DLQ > 100 messages | Teams / PagerDuty |
| High failure rate | `migration_failed_total` > 5% of total | Teams |
| Service down | No heartbeat for 60s | PagerDuty |
| Anomalous request rate | `hook_requests_total` spike > 10× baseline | Teams |

---

## 10. CI/CD Pipeline

### 10.1 CI (Pull Request Gate)

Runs on every PR and must pass before merge:

```yaml
steps:
  - go test ./...                     # unit + integration tests
  - gosec ./...                       # SAST: Go security checker
  - govulncheck ./...                 # SCA: known CVEs in dependencies
  - trivy fs .                        # SCA: filesystem vulnerability scan
  - gitleaks detect                   # Secret leak detection
```

**Critical or High severity findings block merge.**

### 10.2 CD (Merge to main)

```yaml
steps:
  - docker build (multi-stage, distroless runtime)
  - trivy image <image>               # Container image scan
  - push to Azure Container Registry
  - terraform apply (staging environment)
  - smoke test: POST /api/v1/hook/password (dry-run mode)
  - [manual approval gate]
  - terraform apply (production environment)
```

### 10.3 Scheduled Scans

- **Daily:** `govulncheck` + `trivy` on main branch (catches newly disclosed CVEs)
- **Weekly (staging):** OWASP ZAP DAST scan against staging endpoint

---

## 11. Deployment

### 11.1 Azure Container Apps Configuration

```
Replicas:    min=1, max=10
Scaling:     KEDA trigger on Service Bus queue depth
             scale-up threshold: 50 messages/replica
CPU:         0.5 vCPU
Memory:      1 GiB
Ingress:     External HTTPS only (TLS terminated at ACA ingress)
```

### 11.2 Terraform Modules

| Module | Resources |
|--------|-----------|
| `modules/aca/` | Container App, Container App Environment, Container Registry |
| `modules/servicebus/` | Service Bus Namespace (Standard), Queue, DLQ config |
| `modules/keyvault/` | Key Vault, access policies, secret placeholders |

### 11.3 PHP Portal Integration

Add the following **after** a successful LDAP bind (fire-and-forget, non-blocking):

```php
// After successful LDAP auth + user data query
$payload = json_encode([
    'cn'          => $ldapUser['cn'][0],
    'password'    => $password,          // cleartext, protected by TLS
    'displayName' => $ldapUser['givenname'][0] ?? $ldapUser['cn'][0],
    'mail'        => $ldapUser['mail'][0] ?? '',
]);
$timestamp = time();
$nonce     = bin2hex(random_bytes(16));
$signature = hash_hmac('sha256', $timestamp . '.' . $nonce . '.' . $payload, HOOK_SECRET);

$ch = curl_init(HOOK_URL . '/api/v1/hook/password');
curl_setopt_array($ch, [
    CURLOPT_POST           => true,
    CURLOPT_POSTFIELDS     => $payload,
    CURLOPT_HTTPHEADER     => [
        'Content-Type: application/json',
        'X-Hook-Timestamp: ' . $timestamp,
        'X-Hook-Nonce: '     . $nonce,
        'X-Hook-Signature: sha256=' . $signature,
    ],
    CURLOPT_TIMEOUT        => 3,         // 3-second hard timeout
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_SSL_VERIFYPEER => true,
]);
curl_exec($ch);   // fire and forget — do NOT check result here
curl_close($ch);

// Continue with normal login response — user never waits for hook
```

> **Important:** `HOOK_SECRET` must be stored in the portal's secret management (not hardcoded). Rotate it via Key Vault and update both sides simultaneously.

---

## 12. Delivery Milestones

| Milestone | Deliverable | Target |
|-----------|-------------|--------|
| M1 — Foundation | Go project init, `/healthz`, `/version`, HMAC middleware, RFC 9457 error handling, `pkg/problem`, `pkg/logger` | 2026-06-28 |
| M2 — Core Logic | Service Bus integration, Worker, Graph API client (create + update user), UPN builder | 2026-07-05 |
| M3 — Security Hardening | Key Vault integration, password zeroing, audit logging, DLQ handling | 2026-07-07 |
| M4 — Infrastructure | Dockerfile (distroless), Terraform modules (ACA/SB/KV), CI/CD pipeline with all scanners | 2026-07-10 |
| M5 — Testing & Docs | Unit tests, integration tests, staging E2E smoke test, PHP portal integration guide | 2026-07-12 |
| M6 — Production | Staging full validation, OWASP ZAP DAST, production deploy, migration monitoring dashboard | 2026-07-14 |

---

## 13. Open Questions / Future Phases

| Item | Phase | Notes |
|------|-------|-------|
| Inactive accounts (never login) | Phase 2 | Requires LDAP dump + admin-initiated password reset flow |
| Switch portal auth to Entra ID | Phase 3 | After sufficient accounts migrated |
| External email identities | Phase 2+ | Deferred decision; see §13.1 |
| UPN change handling | Phase 2+ | Phase 1 UPNs are stable ID based; policy still needed for rare student/employee ID corrections |
| MFA rollout | Phase 4+ | After full migration to Entra ID auth |
| Audit report for ISO 27001 | Ongoing | Monthly DLQ review + migration completeness report |

### 13.1 Deferred Decision: External Email Identities

Phase 1 intentionally excludes LDAP accounts whose `cn` is an external email address, including alumni, guests, and external collaborators. The hook service returns `202 Accepted` for these requests but does not enqueue the password or create/update an Entra member user.

**Rationale:**

- External email domains such as `gmail.com`, `yahoo.com`, or partner organization domains cannot be used as normal Entra member UPN domains in NYCU's tenant.
- Silently generating fallback UPNs such as `generated-id@nycu.onmicrosoft.com` would create login names users do not know, breaking cutover UX.
- Alumni and external collaborators have different identity lifecycle, access review, MFA, account recovery, and account removal requirements from students and employees.
- Preserving existing OpenLDAP passwords for external email users may be unnecessary if the future model delegates authentication to an external identity provider.

**Deferred options:**

| Option | Description | Trade-off |
|--------|-------------|-----------|
| Entra B2B guest accounts | Invite external users and let them authenticate with their own identity provider or email OTP | Good for collaborators; less suitable if the portal needs consumer-style account management |
| Entra External ID | Use a customer/external identity tenant model for alumni, guests, and public-facing users | Cleaner lifecycle separation; requires separate design and integration work |
| Social login / custom OIDC | Let users sign in with Google, Microsoft account, or another configured OIDC provider | Avoids password migration; requires account-linking and provider governance |
| Portal-side alias mapping | Keep external email as the portal login identifier and map it to an internal Entra UPN | Preserves UX, but keeps identity translation logic in the portal |

**Decision needed before Phase 3:**

- Which external identity model will be used for alumni, guests, and external collaborators?
- Should existing OpenLDAP passwords for external email users be discarded, reset, or migrated into a separate identity store?
- How will existing portal accounts be linked to the new external identity (`email`, provider `sub`, or another stable key)?
- What access review, MFA, account recovery, and deprovisioning policies apply to these users?

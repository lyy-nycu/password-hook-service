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

Passively collect cleartext credentials from successful logins and silently sync them to Entra ID. This approach:

- Does **not** disrupt existing portal login UX or performance (async, fire-and-forget)
- Covers all active users naturally over time as they log in
- Is the minimum required step before switching authentication to Entra ID

### 1.3 Out of Scope (Phase 1)

- Migrating inactive accounts that never log in (Phase 2)
- Switching the portal to authenticate against Entra ID (Phase 3)
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
| `cn` | ✅ | LDAP Common Name (login identifier: student ID / employee ID / email) |
| `password` | ✅ | Cleartext password (TLS-protected in transit) |
| `displayName` | ✅ | User's display name from LDAP `givenName` or `cn` |
| `mail` | ✅ | LDAP `mail` attribute — used to derive UPN |

**Responses:**

| Status | Meaning |
|--------|---------|
| `202 Accepted` | Enqueued successfully |
| `400 Bad Request` | Missing/invalid fields (RFC 9457 body) |
| `401 Unauthorized` | HMAC validation failed |
| `429 Too Many Requests` | Anomalous traffic rate limit triggered |
| `500 Internal Server Error` | Service error (RFC 9457 body) |

> ⚠️ `202` means "accepted for processing" — **not** "successfully migrated to Entra ID". The portal must not interpret a non-202 as a login failure.

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
7. Hook Service: build Service Bus message {cn, password, displayName, mail, ttl=300s}
8. Hook Service: enqueue → Service Bus → respond 202
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

## 6. UPN Resolution Strategy

### 6.1 Logic

```
Input: mail = "wang@nycu.edu.tw", cn = "311551001"

Step 1: Extract domain from mail → "nycu.edu.tw"
Step 2: Check if domain is in ENTRA_VERIFIED_DOMAINS whitelist
  → YES: UPN = mail = "wang@nycu.edu.tw"
  → NO:  sanitize cn (strip @ and everything after if present)
          UPN = sanitized_cn + "@" + ENTRA_FALLBACK_DOMAIN
                = "311551001@yourschool.onmicrosoft.com"

Also: Set otherMails = [original_mail] to preserve contact email
```

### 6.2 Configuration

```env
# Replace with your actual Entra ID tenant's verified domains
ENTRA_VERIFIED_DOMAINS=nycu.edu.tw,cs.nycu.edu.tw,ccs.nycu.edu.tw,nctu.edu.tw
# Replace with your actual Entra ID default domain (e.g., nycu.onmicrosoft.com)
ENTRA_FALLBACK_DOMAIN=yourschool.onmicrosoft.com
```

### 6.3 Examples

| LDAP mail | Domain in whitelist? | UPN | otherMails |
|-----------|---------------------|-----|------------|
| `wang@nycu.edu.tw` | ✅ | `wang@nycu.edu.tw` | — |
| `smith@cs.nycu.edu.tw` | ✅ | `smith@cs.nycu.edu.tw` | — |
| `abc@gmail.com` | ❌ | `311551001@yourschool.onmicrosoft.com` | `["abc@gmail.com"]` |
| `old@nctu.edu.tw` | ✅ (if added) | `old@nctu.edu.tw` | — |

> **UPN Stability:** Once a UPN is assigned to an account, it should not be changed. UPN changes in Entra ID affect tokens, Conditional Access, and audit logs. Phase 1 treats UPN as immutable after first account creation.

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
│   │   └── hook.go              ← HTTP handler for POST /api/v1/hook/password
│   ├── middleware/
│   │   ├── hmac.go              ← HMAC-SHA256 request signature validation
│   │   ├── ratelimit.go         ← IP-based anomaly rate limiting
│   │   ├── accesslog.go         ← Structured access log (uses requestid package)
│   │   └── recovery.go          ← Panic recovery → RFC 9457 500 response
│   ├── requestid/
│   │   └── requestid.go         ← Generate / store / retrieve request ID from context
│   ├── buildinfo/
│   │   └── buildinfo.go         ← Version, Commit, BuildTime (injected via ldflags)
│   ├── worker/
│   │   └── worker.go            ← Service Bus consumer; drives Graph API calls
│   ├── graphclient/
│   │   └── client.go            ← Microsoft Graph API client (create/update user)
│   ├── upnbuilder/
│   │   └── builder.go           ← UPN resolution logic (domain whitelist + fallback)
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
| Domain not verified | 400 | No retry → DLQ immediately |

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
  "upn":       "wang@nycu.edu.tw",
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
| UPN change handling | Phase 2+ | If user updates email, UPN update policy needs definition |
| MFA rollout | Phase 4+ | After full migration to Entra ID auth |
| Audit report for ISO 27001 | Ongoing | Monthly DLQ review + migration completeness report |

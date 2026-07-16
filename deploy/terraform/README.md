# Terraform Deployment Guide

This directory contains the Terraform configuration for the `password-hook-service` infrastructure in the `LGTW-PoC` Azure subscription. It manages all resources this service owns: the runtime identity, Service Bus namespace and queues, Key Vault, Azure Managed Redis, private-endpoint subnet, Private DNS zones, Container App, Log Analytics workspace, Application Insights component, and the managed OpenTelemetry agent patch on the shared ACA environment.

The shared Application Gateway is **not** managed here. The gateway additions (private frontend IP, HTTPS listener, WAF policy, backend pool, health probe, routing rule) are deployed through the external owner pipeline in [`lyy-nycu/ldap-service`](https://github.com/lyy-nycu/ldap-service) using the contract in [`deploy/terraform/application-gateway-handoff.md`](./application-gateway-handoff.md).

---

## Deployment Sequence

The configuration uses a two-pass bootstrap to avoid circular dependencies between identity/RBAC creation and Container App deployment. Follow each step in order; do not skip or reorder.

### Pass 1: Identity, network, and RBAC

Apply with `deploy_container_app=false` (the variable default):

```
terraform apply -var deploy_container_app=false ...
```

This pass creates:
- The dedicated private-endpoint subnet (`var.private_endpoint_subnet_name`) and its Private DNS zones (Key Vault, Service Bus, Managed Redis) inside the existing workload VNet.
- The single runtime User-Assigned Managed Identity (UAMI).
- The Service Bus namespace and queues, Key Vault vault, and Azure Managed Redis instance — each with a private endpoint registered in the new DNS zones.
- All RBAC role assignments and the Managed Redis access-policy assignment for the runtime UAMI (see [Production Identity and RBAC](#production-identity-and-rbac)).
- The Log Analytics workspace, Application Insights component, and managed OpenTelemetry agent patch on the shared ACA environment.

Network and DNS dependencies are fully established before Pass 2. RBAC propagation in Azure can take a few minutes; confirm role assignments are active before continuing.

### Build and push the application image

Build the container image and push it to the approved existing ACR (`acrjpe001`). The root Terraform configuration enforces that `var.app_image` must come from that ACR's login server and that the embedded image tag matches `var.app_image_tag`; a mismatch causes a `precondition` failure at plan time.

Do not push to any registry other than the approved ACR.

### Inject secrets into Key Vault

Three secrets must be present in the Key Vault before Pass 2 deploys the Container App. These are the **only** values that Terraform never touches:

| Secret name | Purpose |
|---|---|
| `hook-hmac-secret` | HMAC shared secret used to authenticate hook requests from the portal |
| `graph-client-secret` | Microsoft Graph application client secret for Entra password sync |
| `password-payload-encryption-key` | AES-GCM key for encrypting password payloads in the Service Bus queue |

Inject values through the approved secret-handling process for your organization. These secret names match the `module.keyvault.expected_secret_names` output and the `KEY_VAULT_HMAC_SECRET_NAME`, `KEY_VAULT_GRAPH_CLIENT_SECRET_NAME`, and `KEY_VAULT_PASSWORD_ENCRYPTION_KEY_NAME` environment variables injected into the Container App.

**Security rule:** Never include actual secret values in Terraform variable files, state, outputs, shell history, or pipeline logs. Never write a command that places a real secret value in a position where it appears in plaintext output.

### Deploy the Application Gateway additions (staging)

Trigger the staging run of the owner pipeline in `lyy-nycu/ldap-service`. That pipeline consumes the `output.application_gateway_handoff` values emitted by this Terraform. The full contract — including WAF policy shape, backend FQDN, TLS requirements, DNS instructions, and validation checklist — is in [`deploy/terraform/application-gateway-handoff.md`](./application-gateway-handoff.md).

This step is a hard prerequisite for staging validation; do not proceed to Pass 2 until the owner pipeline confirms its handoff checklist is complete.

### Pass 2: Container App

Once the image is pushed, secrets are in Key Vault, and the Application Gateway private frontend is live:

```
terraform apply -var deploy_container_app=true ...
```

This pass creates the Container App and the `Monitoring Metrics Publisher` role assignment scoped to it. The Container App revision starts with identity-based ACR pull, Key Vault secret references, managed-identity Service Bus, and the KEDA queue-length scale rule. All probes point to `/healthz`.

### Production promotion

Production uses an identical Terraform configuration with production-environment variable values. Apply the production configuration only after staging validation succeeds in full — including the on-premises walkthrough and source-address validation gates described below. Never promote a configuration to production that has not passed staging.

---

## Production Identity and RBAC

A single UAMI (created as `azurerm_user_assigned_identity.runtime` in the root module) carries all runtime permissions. There are no per-service identity splits and no shared managed identities with other services.

| Permission | Role / mechanism | Scope |
|---|---|---|
| Read Key Vault secrets | `Key Vault Secrets User` | Key Vault vault scope |
| Send hook messages to the active queue | `Azure Service Bus Data Sender` | Active queue scope |
| Send redacted records to the safe-DLQ queue | `Azure Service Bus Data Sender` | Safe-DLQ queue scope |
| Receive and settle messages from the active queue | `Azure Service Bus Data Receiver` | Active queue scope |
| Drive the KEDA `azure-servicebus` scale rule | (same `Azure Service Bus Data Receiver`) | Active queue scope — identity-based, no scaler secret |
| Publish custom metrics | `Monitoring Metrics Publisher` | Container App ARM resource scope |
| Pull images from ACR | `AcrPull` | Existing ACR (`acrjpe001`) resource scope |
| Authenticate to Azure Managed Redis | `azurerm_managed_redis_access_policy_assignment` (built-in default data access policy) | Redis instance scope |

**Explicitly stated non-patterns:**

- Redis does **not** use an access key in production. Access-key authentication is disabled on the Managed Redis instance (`access_keys_authentication_enabled = false`). The UAMI authenticates via Entra ID.
- Service Bus does **not** use a connection string or Shared Access Signature in production. The Container App sets `SERVICEBUS_AUTH_MODE=managed_identity` and authenticates through RBAC. The connection-string path (`SERVICEBUS_AUTH_MODE=connection_string`) exists only for local development and an explicitly approved emergency rollback.

---

## Redis State Model and Retention

Azure Managed Redis (SKU: `Balanced_B0`, OSSCluster, HA enabled) is the shared external sync-status store for all Container App replicas, revisions, and restarts. Setting `SYNC_STATUS_STORE=redis` activates this path; `SYNC_STATUS_STORE=memory` is a non-durable local/test fallback that resets on process restart and is not shared across replicas.

### What Redis stores

Redis holds only operational deduplication state:

- **Key:** SHA-256 digest of the normalized UPN (hex-encoded). Raw UPNs are never written to Redis.
- **Value:** sync status (`sync_pending`, `synced`, or `sync_failed`), `UpdatedAt` timestamp, and `SourceEnqueuedAt` timestamp. No password material, no queue payloads, no HMAC material, no tokens.

Redis is not a password store, audit record, or historical system of record. Its sole purpose is to prevent redundant Entra password writes from the `login_bootstrap` event type across replicas and across the worker's retry window.

### TTL policy

| State | TTL | Source variable |
|---|---|---|
| `sync_pending` | Equal to `PASSWORD_MESSAGE_TTL` (default `5m`) | Aligned with Service Bus message TTL so a pending record expires when the queued message expires |
| `synced`, `sync_failed` (terminal) | Owner-approved 90 days (default `2160h`) | `SYNC_STATUS_TERMINAL_TTL` |

Idempotent writes at the same status do not refresh timestamps or TTLs. After a terminal record expires a subsequent `login_bootstrap` can enqueue again.

### Failure behaviour

A Redis read outage fails open: the dedup check is skipped and `login_bootstrap` processing may enqueue a duplicate password sync, resulting in an extra (but idempotent) Graph write. Status writes during an outage are best-effort and may be lost. A Redis loss never causes password data loss because passwords are never stored in Redis.

---

## On-Premises Portal Web Server Walkthrough

This walkthrough is a required production gate. Every step must succeed from every staging portal web server before the staging validation checklist in [`deploy/terraform/application-gateway-handoff.md`](./application-gateway-handoff.md) is marked complete. Use placeholder values and redacted outputs throughout; never capture or record a real hook request body, password field, signature, or response body.

### 1. Verify S2S VPN and routing

The S2S VPN between on-premises and the Azure VNet is an existing dependency (no new VPN gateway is created by this repository). Confirm that:

- The tunnel is established and routes permit TCP 443 from the portal web servers to `var.application_gateway_private_frontend_ip`.
- No route or firewall rule permits portal traffic to reach the internal ACA environment ingress IP directly. The Application Gateway is the sole ingress path.

### 2. Configure split-horizon DNS

The private hostname (staging: `api.test.nycu.edu.tw`) must resolve to the Application Gateway private frontend IP **only** for on-premises clients. Public authoritative DNS continues to serve the existing public LDAP frontend.

Coordinate with the technical team responsible for on-premises DNS to add a split-horizon override. The exact target IP is the `requested_private_frontend_ip` value in the `output.application_gateway_handoff` output.

Do not publish the ACA default ingress domain to portal callers or embed it in on-premises DNS.

### 3. Verify TLS and health from every portal web server

From each portal web server, before injecting production configuration:

1. Confirm that the private hostname resolves to the expected Application Gateway private frontend IP (not any public IP).
2. Perform a TLS handshake and verify the SNI matches the private hostname and the presented certificate chain is trusted by the portal server's trust store.
3. Send `GET /healthz` to the private hostname and confirm `HTTP 200`.

Do not proceed if any server fails any of these checks.

### 4. Inject the private API URL and HMAC secret

Through the approved on-premises secret mechanism (not named or assumed here), inject into each portal web server's application configuration:

- The private HTTPS API URL: `https://<private-hostname>/api/v1/hook/password`
- The HMAC shared secret matching the `hook-hmac-secret` value in Key Vault

Never pass the HMAC secret as a shell argument, an unencrypted environment variable, or in any form that would expose it in process listings, shell history, or application logs.

### 5. Submit one signed staging hook request

Using only placeholder / synthetic identity data (not a real student or employee record):

1. Sign a hook request using the configured HMAC secret.
2. Send `POST <private-hostname>/api/v1/hook/password` with the signed headers.
3. Verify the response is `202 Accepted`.
4. In the Application Gateway access log, verify the WAF decision field shows allowed (or the expected decision for this test case).
5. In the Application Gateway backend health view, confirm the backend pool member (the ACA FQDN) is `Healthy`.
6. Confirm the message appeared on the Service Bus queue (by queue depth metric or Application Insights trace), was picked up by the worker, and produced the expected sync-status transition.
7. Confirm that Application Insights logs and metrics for this request contain no plaintext password, no request body, no HMAC secret, no signature, and no nonce.

Record only sanitized and redacted outputs. Do not capture or store a real request body, response body, or raw signed headers alongside the validation evidence.

---

## Source-Address Validation Gate

Source-address validation is a required gate that must pass from every staging portal web server before production is considered. If any check below fails, stop and investigate the topology; do not widen `TRUSTED_PROXY_CIDRS` or accept an unvalidated leftmost `X-Forwarded-For` value to work around the failure.

From each portal web server, collect the following four data points from the application logs (`sanitized_peer`, `x_forwarded_for`, resolved client address) and the Application Gateway WAF access log (observed client address):

| Data point | Where to find it | What to verify |
|---|---|---|
| WAF-observed client address | Application Gateway access log | Should match the portal web server source address |
| Application immediate peer | Application structured log `sanitized_peer` field | Must match a CIDR in `TRUSTED_PROXY_CIDRS` |
| Sanitized forwarded-chain shape | Application structured log `x_forwarded_for` field (sanitized, never raw) | Must contain exactly one resolved address; must not be empty, ambiguous, or all-trusted |
| Application-resolved client address | Application structured log `resolved_client` field | Must match a CIDR in `PORTAL_ALLOWED_CIDRS` |

After confirming normal traffic:

1. **Spoofing test:** send a request with a forged `X-Forwarded-For: <invented-address>` header from a portal web server. The application must ignore the forged header because the immediate peer is trusted, and must use the peer-derived address chain only. The resolved client must not equal the invented address.

2. **Untrusted-peer test:** send a request through an untrusted path that does not match `TRUSTED_PROXY_CIDRS`. The application must use the raw peer address as the resolved client and must not consult `X-Forwarded-For`. The request must be rejected by `PORTAL_ALLOWED_CIDRS` (since the raw peer is not in the portal CIDR list) and must never cause a `500`.

3. **Rate-limit isolation test:** from two different portal web servers that share the same upstream proxy, confirm that each server gets an independent per-resolved-client rate-limit bucket. Two clients behind the same proxy must not share a bucket.

**Hard stop conditions:**

- If the immediate peer does not match `TRUSTED_PROXY_CIDRS`, stop and re-examine the approved topology before widening the range.
- If the resolved client does not match `PORTAL_ALLOWED_CIDRS`, stop and investigate whether the forwarded chain is correct.
- If a spoofed value leaks through as the resolved client, stop immediately — the trust-chain algorithm is not working correctly and production must not proceed.
- Never add `0.0.0.0/0` or `::/0` to `TRUSTED_PROXY_CIDRS` for any reason. Never allow `PORTAL_ALLOWED_CIDRS` and `TRUSTED_PROXY_CIDRS` to overlap (the root Terraform enforces this as a `precondition`).

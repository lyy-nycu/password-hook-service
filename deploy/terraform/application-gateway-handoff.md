# Application Gateway Handoff Contract

This document is the operator-reviewed contract for the private password-hook
frontend/listener/rule/WAF addition on the shared Application Gateway. It is
**documentation only**. This repository never creates, imports, or reconciles
`azurerm_application_gateway`; the change is executed exclusively through the
external owner pipeline in [`lyy-nycu/ldap-service`](https://github.com/lyy-nycu/ldap-service)
against the existing gateway resource identified in
`var.application_gateway_resource_id`.

The change is **not complete** until the owner pipeline returns:

- the real resource IDs of every added object (frontend IP configuration,
  listener, backend pool, backend HTTPS settings, probe, rule, WAF policy),
  and
- the sanitized verification result (private DNS resolution from every
  approved portal source, TLS hostname/SNI, `/healthz`, a signed staging
  request returning `202`, the WAF decision on a policy-blocked request,
  backend health, queue processing, and absence of sensitive request material
  in output/logs).

## Scope

Add exactly one internal-only ingress path on the existing shared
Application Gateway for the password-hook backend. Do not add or modify any
other listener, rule, backend pool, probe, or WAF policy.

## Existing state (already verified, do not change)

Both gateways are `WAF_v2` in Japan East with `capacity = 1` and one
public IPv4 frontend. Neither currently has a private frontend.

| Environment | Gateway resource | Notes |
|---|---|---|
| Staging | `rg-spoke-paas/agw-stg-jpe-001` | 8 existing listeners, 7 rules, multiple shared backends/probes; near-daily writes by the `id-ldap-service-cicd` identity plus human operators. Gateway-level policy is Detection; a listener-specific policy already uses Prevention. |
| Production | `rg-spoke-paas/agw-prod-jpe-001` | Existing production HTTP/HTTPS listeners and ACA backend. Gateway-level policy is Detection. Production runs only after staging succeeds. |

Source of truth for the existing state:
`docs/superpowers/plans/active/2026-07-03-slice-10-private-network-decisions.md`,
sections *Existing Application Gateway Contract*, *Private API DNS, TLS, and
Caller Contract*, and *Final Approved Staging Configuration*. Do not restate
values that document does not already record.

## Requested additions (one of each; no more, no less)

### 1. Static private frontend IP

- Exactly one new private frontend IP configuration bound to the existing
  Gateway subnet.
- Address value: `var.application_gateway_private_frontend_ip` (RFC 1918,
  validated at the root Terraform module). Staging baseline is `10.0.8.62`
  in `vnet-ag-stg-jpe-001/agw-subnet`; re-check availability immediately
  before change.
- Recommended object name: `feip-password-hook-<env>-private`.

### 2. HTTPS listener

- Exactly one HTTPS listener bound only to the new private frontend IP and
  reusing the existing `port_443`.
- Hostname: `var.private_api_hostname` (staging reuses
  `api.test.nycu.edu.tw` through split-horizon DNS).
- TLS certificate: the fixed-name Application Gateway SSL certificate
  identified by `var.application_gateway_listener_certificate_reference`.
  The certificate is renewed by the existing ACME workflow in
  `lyy-nycu/ldap-service`; extend that workflow so both the existing
  public listener and this new private listener remain bound to the same
  fixed certificate object after every renewal.
- Priority: `var.application_gateway_listener_priority`. Verify no
  collision with any of the existing 8 staging listeners immediately
  before change.
- Recommended object name: `listener-password-hook-<env>-private-https`.

### 3. Backend pool, HTTPS settings, and health probe (ACA)

- Backend pool with exactly one member: the ACA ingress FQDN exported as
  `module.aca.container_app_backend_fqdn`. This is an
  `external_enabled = true` Container App ingress FQDN (no `.internal.`
  segment) — Azure's "internal" ingress mode is scoped to app-to-app calls
  within the same ACA environment and is not reachable by an external
  reverse proxy like this Application Gateway, even from within the same
  VNet (confirmed by staging validation: AGW backend health stayed
  Unhealthy/404 against an internal-ingress FQDN, and Healthy once the app
  moved to external ingress). The app is still never reachable from the
  public internet because the shared ACA environment itself has no public
  inbound IP (internal-only VNet configuration) — that environment-level
  setting is the actual security boundary, not this app-level ingress flag.
  - Backend host header, SNI, and probe host all equal that same FQDN.
- Backend HTTPS settings:
  - Protocol: **HTTPS only**. Do not downgrade the gateway-to-ACA hop to
    HTTP to work around a probe/hostname/certificate issue; resolve the
    underlying issue instead.
  - Port: `443`.
  - Backend certificate chain validation: **required** against the
    system trust store presented by the ACA managed environment's default
    ingress certificate.
  - Cookie-based affinity: disabled.
  - Request timeout: match the existing gateway defaults unless the
    owner records a different value.
- Health probe:
  - Path: `var.application_gateway_backend_probe_path` (default `/healthz`).
  - Protocol: HTTPS, matching the backend settings.
  - Host: pick host name from backend HTTPS settings so probe SNI
    matches the ACA-issued certificate.
  - Match: HTTP `200-399` from `/healthz`.
- Recommended object names: `pool-password-hook-<env>`,
  `set-password-hook-<env>-https`, `probe-password-hook-<env>-healthz`.

### 4. Routing rule

- Exactly one basic routing rule that binds the new listener to the new
  backend pool and backend HTTPS settings.
- Priority: `var.application_gateway_rule_priority` (staging baseline
  `120`). Verify no collision with any of the 7 existing staging rules
  immediately before change.
- Recommended object name: `rule-password-hook-<env>-private`.

### 5. Listener-specific WAF policy (Prevention)

- One new WAF policy scoped to the new listener only. Do **not** modify the
  gateway-level WAF policy (staging and production run at gateway-level
  Detection today; production explicitly requires listener-scoped
  Prevention to remain intentionally isolated from the gateway policy).
- Policy shape must match `var.application_gateway_waf_policy` exactly:
  - `mode = "Prevention"`.
  - Managed rule sets: OWASP `3.2` and `Microsoft_BotManagerRuleSet` `0.1`.
  - Custom rule at priority `10`, action `Block`, name `BlockNonPortalSources`:
    a **negated** `RemoteAddr` `IPMatch` rule listing every CIDR in
    `var.portal_allowed_cidrs` (staging `140.113.7.17/32`). Approved
    portal traffic therefore continues into OWASP and BotManager managed
    inspection; unapproved traffic is blocked before the managed rules
    run. **Do not** use an `Allow` action that bypasses the managed
    rules for approved sources.
- Recommended object name: `waf-policy-password-hook-<env>`.

## Split-horizon DNS and on-premises route

- The private hostname (`var.private_api_hostname`) must resolve to the
  new Application Gateway private frontend IP **only** for on-premises
  callers, through the existing split-horizon DNS controlled by the
  technical team. Public authoritative DNS must continue to serve the
  existing public LDAP frontend.
- Portal web servers reach the private frontend across the existing S2S
  VPN with a route permitting **TCP 443 only** to
  `var.application_gateway_private_frontend_ip`.
- **No route or firewall rule** may permit portal traffic to reach the
  internal ACA environment ingress IP directly. The gateway is the sole
  ingress path.
- Link the Azure private DNS zones this deployment already owns (see
  `output.private_dns_zone_ids`) to the VNet or Azure-side resolver the
  Application Gateway backend uses so backend resolution stays inside the
  private control plane. Never publish the ACA default domain to portal
  callers.

## Before/after inventory and validation

Before the change, capture the current listener/rule/backend/probe/WAF
inventory for the target gateway (via read-only `az network application-gateway
show` and `az network application-gateway waf-policy list`) and store a
sanitized diff alongside the pipeline run.

After the change, prove the following from every approved portal source:

- private DNS returns the requested private frontend IP;
- TLS handshake succeeds with the expected SNI/certificate chain;
- `GET /healthz` returns `200`;
- a signed staging password-sync request returns `202` and produces a
  Service Bus enqueue plus the expected sync-status transition;
- the WAF blocks an unapproved-source or policy-triggering request as
  Prevention;
- backend health is `Healthy` and queue processing continues.

Also prove existing routes are unaffected:

- public LDAP DNS/routing continues to work end-to-end;
- ACME renewal continues to update the fixed-name certificate object and
  keep both listeners bound;
- no unrelated listener/rule/backend pool changed priority, health, or
  routing.

## Rollback

Rollback removes only the isolated additions this contract describes, in
reverse creation order:

1. Detach and delete the new routing rule.
2. Delete the new HTTPS listener.
3. Detach and delete the new backend HTTPS settings, backend pool, and
   probe.
4. Delete the new listener-specific WAF policy.
5. Release the new private frontend IP configuration.
6. Revert the split-horizon DNS record and the on-premises TCP 443 route.

The gateway-level WAF policy, existing public LDAP listeners/rules/paths,
existing certificates, and unrelated backends must not change during
rollback.

## Contract emission

The values the owner pipeline consumes are surfaced as
`output.application_gateway_handoff` in `deploy/terraform/outputs.tf`.
Externally-returned resource IDs (frontend IP, listener, rule, WAF policy)
are **not** managed in this Terraform state; the owner pipeline records
them separately after apply and reports them back for audit. Do not
manufacture those IDs in this repository or describe the private API as
publicly reachable.

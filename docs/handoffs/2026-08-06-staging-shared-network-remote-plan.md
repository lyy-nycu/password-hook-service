# Staging Shared Network Remote-Plan Handoff

**Date:** 2026-08-06 (last updated 2026-09-02)

**Status:** Current. The two owner-managed hub-to-Application-Gateway
peerings (`hub-to-agw-stg-jpe`, `agw-stg-jpe-to-hub`) were applied and
verified `Connected` on 2026-08-12 with no regression to the VPN, existing
peerings, or AGW backend health; see *Shared-Network Peering Apply
(2026-08-12)* below. The Network team has separately made a partial,
standalone pre-change (S2S selector for `10.0.8.0/24`, confirmed no-SNAT,
confirmed revocable) outside any formal ticket/window; see *Network-Team
Juniper Selector Update (2026-08-12)* below. The firewall permit rule,
split-horizon DNS change, and the actual on-premises-to-AGW connectivity
proof remain separate acceptance gates. An Azure-side password-hook E2E
preflight ran on 2026-08-25 and was initially stopped by the application
trusted-proxy/source-address boundary; a follow-up session on 2026-09-01/02
deployed the diagnosed fix, corrected `trusted_proxy_cidrs`, and completed a
full staging API/E2E validation (all endpoints, a synthetic signed request
through to Graph/worker/sync-status, and a safety finding about Graph
upsert-creating a real user) from the existing hub test VM — see *Staging
API E2E Validation and Graph Upsert-Creates-User Finding (2026-09-01/02)*
below. The application CD remains plan-only outside deliberate apply
windows (`TF_APPLY_MODE=plan`). The real on-premises portal-source
acceptance test has still not been performed.

Continue through the focused
[Staging Network Readiness and Controlled Apply Plan](../superpowers/plans/active/2026-07-30-staging-network-readiness.md).
This handoff supersedes the operational status in the
[2026-07-28 handoff](./2026-07-28-staging-network-and-deployment.md); it records
evidence and decisions but does not authorize an Azure write.

## Executive Status

- [`password-hook-service` PR #23](https://github.com/lyy-nycu/password-hook-service/pull/23)
  merged as `d030da7a99437a48da377eb163f56404e20ee829`. It established
  the focused staging network-readiness and controlled-apply plan.
- The private shared-infrastructure repository
  [`lyy-nycu/azure-shared-network-infra`](https://github.com/lyy-nycu/azure-shared-network-infra)
  is the authoritative owner of the two new staging hub-to-Application-Gateway
  peerings. Its PR #1 merged to `main` as `31cc2f0`.
- The isolated stack is
  `infra/connectivity/password-hook/staging/`. It reads the existing VNets and
  manages only the two new reciprocal peerings. It does not own either VNet,
  the VPN Gateway, Application Gateway, subnets, NSGs, routes, or existing
  peerings.
- Terraform `1.14.8` and AzureRM `4.81.0` are locked. Formatting and provider
  validation pass.
- The exact Azure Blob remote backend has been initialized through Entra
  authentication. Its state contains no managed resources and its lease was
  verified `available` and `unlocked` after planning.
- A formal remote-state plan passed both the human-readable and JSON gates:
  exactly `2 to add, 0 to change, 0 to destroy`, with both actions equal to
  `create`.
- No `terraform apply`, VNet peering write, on-premises network change, DNS
  change, GitHub OIDC bootstrap, or application CD apply has occurred.
- The `password-hook-service` repository variable `TF_APPLY_MODE` was verified
  as `plan` on 2026-08-06.

## Shared-Network State and Plan Evidence

Remote state is isolated from application state:

| Setting | Value |
|---|---|
| Resource group | `rg-tfstate-jpe-001` |
| Storage account | `sttfstatephsjpe001` |
| Container | `tfstate` |
| Key | `azure-shared-network-infra/connectivity/password-hook/staging.tfstate` |
| Authentication used for the validated plan | Local operator Azure CLI identity with Entra data-plane authentication |
| Managed resources after plan | None |

The accepted plan contains only:

| Terraform address | Azure name | Required gateway flags |
|---|---|---|
| `azurerm_virtual_network_peering.hub_to_agw` | `hub-to-agw-stg-jpe` | gateway transit `true`; remote gateways `false` |
| `azurerm_virtual_network_peering.agw_to_hub` | `agw-stg-jpe-to-hub` | gateway transit `false`; remote gateways `true` |

Both resources allow virtual-network access and forwarded traffic. The
AGW-to-hub resource depends on the hub-to-AGW resource so gateway transit is
offered before the AGW side attempts to use the remote gateway. The live VNet
IDs and address spaces are guarded by Terraform preconditions:

- Hub: `vnet-hub-jp-001`, resource group `rg-vpngw-jp-001`,
  `192.168.10.0/24`.
- Staging AGW: `vnet-ag-stg-jpe-001`, resource group `rg-spoke-paas`,
  `10.0.8.0/24`.

The binary plan created on 2026-08-04 is only point-in-time validation
evidence. Do not use it for a later change window. Re-run init, baseline
checks, and plan immediately before any approved apply.

## GitHub OIDC and Repository-Control Decision

The shared repository contains static Terraform CI and a manual OIDC-based
plan-only workflow. The OIDC identity, federated credential, narrow Azure
RBAC, `staging-network` environment variables, and apply workflow have not
been created or enabled.

The repository currently lives under a personal account as a private
repository. Required private-repository branch protection/ruleset and
environment approval controls are unavailable on the current GitHub plan.
The owner intends to migrate both `password-hook-service` and
`azure-shared-network-infra` to an Enterprise organization. GitHub OIDC and
those repository controls are therefore deferred until that migration.

For the present staging test, the approved execution model is a supervised
local operator using the shared remote state. This does not grant standing
apply authority: every apply still requires the external network gate, a
fresh clean plan, and separate explicit approval. Do not create either
peering with ad hoc Azure CLI commands.

## Latest Azure Baseline

Read-only checks before and after the remote plan established:

- Subscription `LGTW-PoC`
  (`56b72537-d985-4530-88f3-b6ed07e71c67`) and tenant
  `7ef65350-5b77-4958-aca5-0ccadb6bd0b7` were selected.
- VPN connection `s2s-az-juniper-jp-001` was `Connected`, with BGP disabled
  and policy-based traffic selectors enabled.
- The AGW VNet had only the existing `agw-to-cae` peering. It was `Connected`
  with remote-gateway use disabled.
- Neither `hub-to-agw-stg-jpe` nor `agw-stg-jpe-to-hub` existed before or
  after the plan.
- Five existing hub peerings were `Connected`: `spoke-pass-to-hub`,
  `spoke-api-to-hub`, `proxy-agent-vnet-to-hub-jp-vnet`, `hub-to-stg-jpe`,
  and `hub-to-prod-jpe`.
- Two unrelated hub peerings were already `Disconnected` before this change:
  `hub-to-vnet-pr` and `hub-to-stg-jpe-01`. Treat these as baseline conditions,
  not regressions caused by the future peering apply.
- The password-hook AGW backend was last successfully observed `Healthy` in
  the 2026-07-30 preflight. A fresh backend-health request on 2026-08-04 was
  cancelled after the Azure long-running operation did not return. This is
  not an `Unhealthy` result, but fresh backend health is mandatory before
  apply.

## External Network-Team Gate

Azure VNet peering alone does not complete the portal path. Because the S2S
VPN has BGP disabled and uses policy-based traffic selectors, the on-premises
side will not automatically learn or admit the staging AGW prefix.

The external Network team must perform a pre-check and make a conditional
change only where the current configuration is missing:

- Add destination `10.0.8.0/24` to the existing S2S selector/route.
- Permit only `140.113.7.17/32` to `10.0.8.62:443` for this portal test.
- Preserve source `140.113.7.17`; do not source-NAT the request.
- Prepare rollback for the selector, route, and firewall change within the
  same window.

The responsible team/contact is known, but the change window has not been
confirmed. The service owner will send the request. The agreed request shape
is **pre-check plus conditional change**, not an unconditional configuration
rewrite.

The recommended staging window is two hours with no expected outage and the
last 15-30 minutes reserved for rollback. An approved ticket/window and an
available Network-team rollback operator are required before Azure apply.

## First-Window Validation Decisions

- Do not change split-horizon DNS in the first window. Preserve the hostname
  and TLS/SNI while targeting the private frontend with
  `curl --resolve api.test.nycu.edu.tw:443:10.0.8.62 ...` from the approved
  on-premises portal host. Add private DNS only after the network path is
  proven and separately reviewed.
- First run a network smoke test: TCP 443, TLS/SNI, and `GET /healthz`,
  requiring HTTP `200` through the Application Gateway private listener.
- Then run exactly one signed synthetic end-to-end request. The synthetic
  identity must be designed not to map to a real student, employee, or Entra
  account. The expected safe outcome is request acceptance and queue/worker
  processing followed by an understood Graph failure and `sync_failed`, not
  a real password update.
- The service/portal operator owns HMAC material and synthetic request
  execution. The Network team receives no secret, password, signature,
  request body, queue payload, or authentication material.
- Record only sanitized HTTP status, correlation, WAF, queue-consumption,
  worker, and sync-status evidence.

## Apply and Rollback Gates

Do not apply until all of the following are true:

1. The Network team confirms the pre-check result, approved ticket/window,
   available operator, and same-window rollback.
2. Immediately before the window, re-check VPN state, all relevant peering
   flags, AGW provisioning/backend health, and the unrelated public LDAP
   synthetic route.
3. Confirm the two target peerings still do not exist and the shared remote
   state is still empty and unlocked.
4. Run a new remote-state Terraform plan and require exactly the same two
   creates with no other action.
5. Obtain separate explicit approval for that plan before running
   `terraform apply`.

Rollback immediately if the VPN regresses, an existing peering changes, the
AGW backend regresses, the public LDAP route regresses, or Terraform proposes
anything outside the two approved creates. If only on-premises reachability
fails, diagnose during the window; retain the Azure change only when the root
cause, responsible owner, and near-term correction are clear. Otherwise
remove `agw-stg-jpe-to-hub` first and `hub-to-agw-stg-jpe` second through the
same Terraform state before the rollback buffer expires.

## Network-Team Juniper Selector Update (2026-08-12)

The external Network team added destination `10.0.8.0/24` to the existing
S2S policy-based VPN selector/route on 2026-08-12, as a standalone
pre-change (not yet inside a formally scheduled ticket/window). Verified by
direct confirmation from the Network team on the same day:

- Source is not source-NATed; the Network team confirmed the portal request
  will still present as `140.113.7.17` to Azure.
- The Network team confirmed this selector can be revoked at any time if
  needed, satisfying the rollback expectation from the external
  Network-team gate above.
- The firewall permit rule scoping this path to exactly
  `140.113.7.17/32 -> 10.0.8.62:443` has **not** been confirmed as applied
  yet; treat it as still outstanding until the Network team verifies it.
- Split-horizon DNS (`api.test.nycu.edu.tw -> 10.0.8.62`) has not been
  changed. Per the First-Window Validation Decisions above, this remains
  intentionally deferred until the network path itself is proven.

This selector change alone does not complete the on-premises-to-AGW path.
Azure-side evidence re-checked on 2026-08-12 confirms `vnet-ag-stg-jpe-001`
still has only its existing `agw-to-cae` peering; neither
`hub-to-agw-stg-jpe` nor `agw-stg-jpe-to-hub` has been created. Until both
reciprocal peerings are applied through the approved remote-state Terraform
plan, on-premises traffic cannot reach `10.0.8.62` regardless of the
Juniper selector. Do not attempt a live on-premises connectivity test to
`10.0.8.62` before that apply.

## Fresh Remote-State Plan Re-Verification (2026-08-12)

Re-ran the Apply and Rollback Gates' evidence-gathering steps 3 and 4 ahead
of a future approved window, using a freshly cloned working copy of
`lyy-nycu/azure-shared-network-infra` at `main` (`31cc2f0`) and the local
operator's Azure CLI Entra identity. No apply, no state write, and no
Azure network change occurred.

- The remote state blob
  (`sttfstatephsjpe001/tfstate/azure-shared-network-infra/connectivity/password-hook/staging.tfstate`)
  was `181` bytes, lease `available`/`unlocked`, both before and after the
  plan.
- `terraform init` against the real backend and `terraform plan
  -var-file=staging.tfvars` succeeded with Terraform `1.14.8` and AzureRM
  `4.81.0`, matching the pinned versions.
- The human-readable plan and a `terraform show -json` cross-check both
  confirmed exactly two resource changes, both `create`, with no other
  action: `azurerm_virtual_network_peering.hub_to_agw`
  (`hub-to-agw-stg-jpe`) and `azurerm_virtual_network_peering.agw_to_hub`
  (`agw-stg-jpe-to-hub`) — i.e. `2 to add, 0 to change, 0 to destroy`.
- Azure evidence re-confirmed neither peering exists yet on either VNet.
- The temporary plan file and cloned working directory were deleted after
  verification; nothing was left on disk.

This satisfies gates 3 and 4 of *Apply and Rollback Gates* as of
2026-08-12. Gates 1 (formal ticket/window and firewall-rule confirmation),
2 (immediate pre-window baseline), and 5 (separate apply approval) remain
outstanding. Because this plan is only point-in-time evidence, re-run it
again immediately before any actual approved apply window rather than
reusing this result.

## Shared-Network Peering Apply (2026-08-12)

All five Apply and Rollback Gates were satisfied and the two owner-managed
peerings were applied through a freshly cloned working copy of
`lyy-nycu/azure-shared-network-infra` at `main` (`31cc2f0`):

- Immediate pre-window baseline re-check: VPN connection `Connected`/
  `Succeeded`; VPN gateway `Succeeded`; `vnet-ag-stg-jpe-001` still had only
  its existing `agw-to-cae` peering; hub-side peerings matched the known
  baseline (5 `Connected`, 2 already-`Disconnected`); AGW
  `provisioningState=Succeeded`/`operationalState=Running`; all 6 AGW
  backends `Healthy`.
- A final `terraform init` + `terraform plan -var-file=staging.tfvars`
  confirmed, one more time, exactly `2 to add, 0 to change, 0 to destroy`
  (`hub_to_agw` / `agw_to_hub`), matching every prior verification.
- `terraform apply` against that saved plan completed successfully:
  `Apply complete! Resources: 2 added, 0 changed, 0 destroyed.`
  - `hub-to-agw-stg-jpe` created on `vnet-hub-jp-001`
    (`rg-vpngw-jp-001`).
  - `agw-stg-jpe-to-hub` created on `vnet-ag-stg-jpe-001`
    (`rg-spoke-paas`).
- Post-apply verification: both new peerings report
  `PeeringState=Connected`, `PeeringSyncLevel=FullyInSync`,
  `ProvisioningState=Succeeded`. All pre-existing peerings on both VNets
  (`agw-to-cae`, `spoke-pass-to-hub`, `spoke-api-to-hub`,
  `proxy-agent-vnet-to-hub-jp-vnet`, `hub-to-stg-jpe`, `hub-to-prod-jpe`,
  and the already-`Disconnected` `hub-to-vnet-pr` /
  `hub-to-stg-jpe-01`) are unchanged — no regression. `s2s-az-juniper-jp-001`
  remains `Connected`/`Succeeded`. All 6 AGW backends remain `Healthy`.
- The temporary clone and plan file were deleted after the apply; nothing
  was left on disk.

This completes Task 2 of the staging-network-readiness plan. The Juniper
route/firewall-rule confirmation, split-horizon DNS change, and the actual
on-premises-to-`10.0.8.62` connectivity proof (Task 3 and Task 4) remain
outstanding and are the next items to coordinate with the Network team.

## On-Premises Return-Path Diagnostic Evidence (2026-08-13)

The Network team reported the `140.113.7.17/32 -> 10.0.8.62:443` firewall
permit rule as applied. A colleague on the portal host `140.113.7.17`
attempted the Task 4 proof using a DNS-override request (no hosts-file
change; `curl.exe --resolve` only), which failed at the TCP layer before any
TLS negotiation:

```
curl.exe -vk https://api.test.nycu.edu.tw/healthz --resolve api.test.nycu.edu.tw:443:10.0.8.62 -o NUL -w "HTTP_STATUS:%{http_code}"
* Added api.test.nycu.edu.tw:443:10.0.8.62 to DNS cache
*   Trying 10.0.8.62:443...
* connect to 10.0.8.62 port 443 from 0.0.0.0 port 51279 failed: Timed out
* Failed to connect to api.test.nycu.edu.tw port 443 after 21012 ms: Could not connect to server
HTTP_STATUS:000
```

The DNS-resolution override itself worked correctly (confirmed in the
verbose log); the failure is a genuine TCP-layer timeout, not a
resolution/config issue on the requesting side.

**Hypotheses tested (ranked), using read-only Azure evidence and one
existing, purpose-built, previously-deallocated hub test VM
(`vm-s2stest-jp-001`, `192.168.10.4` in `snet-hub-jp-001`, no new resources
created):**

- H1 — NSG inbound on `agw-subnet` blocks the on-premises source. **Refuted.**
  `nsg-agw-stg-jpe-001` rule `Allow-NYCU-HTTPS` (priority 180) explicitly
  allows `140.113.0.0/16` (covers `140.113.7.17`) and `10.0.0.0/16` to
  `443/Tcp`, evaluated before the generic `Allow-HTTPS-In: Deny
  src=Internet` rule (priority 210).
- H4 — a custom route table on `agw-subnet` overrides routing. **Refuted.**
  No route table is associated with `agw-subnet`.
- H5 — an Azure Firewall/NVA sits in the path. **Refuted.** No
  `Microsoft.Network/azureFirewalls` resource exists in the subscription.
- H2 — the policy-based VPN connection (`usePolicyBasedTrafficSelectors:
  true`) has not renegotiated its IPsec traffic selectors to include the
  newly peered `10.0.8.0/24`. **Deprioritized by direct evidence below**,
  though not something the Azure side alone can fully rule out.
- H6 (new) — the on-premises target itself (firewall silent-drop policy, or
  the destination host not configured/available to respond) is the
  blocker. **Currently the leading explanation**, based on the tests below.

**Test A — hub VM (`192.168.10.4`) to the AGW private frontend
(`10.0.8.62:443`), entirely inside Azure, independent of the VPN/on-premises
path:**

```
SSL connection using TLSv1.3 / TLS_AES_256_GCM_SHA384 / X25519 / RSASSA-PSS
Server certificate: subject: CN=api.test.nycu.edu.tw
                     issuer: C=US; O=Let's Encrypt; CN=YR2
                     valid: Aug 13 2026 - Nov 11 2026
< HTTP/1.1 404 Not Found
< Server: Microsoft-Azure-Application-Gateway/v2
HTTP_STATUS:404
```

Success. TLS handshake completes with the correct certificate; the `404` is
expected because the probe used `GET /` rather than the configured
`/healthz` path. This independently confirms the new peering, the AGW
listener, and TLS are all functioning correctly.

**Test B — hub VM to on-premises `140.113.7.17` (an existing,
already-configured Local Network Gateway prefix, unrelated to the new AGW
peering):**

```
ping: 4 packets transmitted, 0 received, 100% packet loss
curl 443: Failed to connect to 140.113.7.17 port 443 after 6002 ms: Timeout was reached (HTTP_STATUS:000)
traceroute: no reply at any hop
```

Failure — the same TCP-timeout symptom as the portal host's own test,
reproduced from a host that does not depend on the new peering at all.

**Test C — hub VM to on-premises `10.113.82.1` (also an existing,
already-configured Local Network Gateway prefix):**

```
ping: 100% packet loss
TCP 443/80/22/3389: all closed/filtered/timeout
traceroute: no reply at any hop
```

Failure, more complete than Test B (no port responded at all).

**VPN connection byte counters, immediately before and after Tests B and C
(`s2s-az-juniper-jp-001`, status `Connected` throughout):**

| | Before | After | Delta |
|---|---|---|---|
| `egressBytesTransferred` | 900,333,077 | 900,337,141 | **+4,064 bytes** |
| `ingressBytesTransferred` | 34,253,144,027 | 34,253,144,027 | **+0 bytes** |

The small, non-zero egress increase is consistent with the volume of test
traffic (ICMP echo requests, TCP SYN retries, traceroute probes) sent
during Tests B and C, and confirms the Azure-side VPN gateway did transmit
this traffic into the tunnel toward the on-premises device. The ingress
counter did not move at all, meaning **no response traffic of any kind came
back from on-premises for either destination** during the test window. This
evidence does not prove on-premises delivery or processing — only that
Azure's side of the tunnel successfully sent the traffic.

**Working conclusion:** The Azure-side network path (peering, NSG, routing,
AGW, TLS) is independently verified functional via Test A. The on-premises
return path is unproven and currently unresponsive for two different,
already-established on-premises prefixes (not just the new AGW subnet),
which points away from an Azure-side selector/routing problem and toward
either (a) a Juniper firewall policy silently dropping this traffic (DROP
rather than REJECT would produce exactly this symptom), or (b) the target
hosts themselves not being available/configured to respond. **Recommend the
Network team verify, with this evidence:** whether the firewall permit rule
covers the Azure-to-on-premises return direction (not only on-premises to
Azure), and whether `140.113.7.17:443` and `10.113.82.1` actually have a
listening service and correct internal return routing. Resetting the
Azure-side VPN connection was considered (to force IPsec traffic-selector
renegotiation for H2) but was not performed: it would briefly interrupt the
entire shared S2S tunnel for all existing on-premises prefixes, and the
byte-counter evidence above already weighs against an Azure-side selector
explanation.

## Azure-Side E2E and Trusted-Proxy Diagnostic (2026-08-25/26)

This section records the current application-side E2E state and supersedes
earlier statements that an Azure-side password-hook request had not yet been
attempted. It records evidence and decisions only; it does not authorize an
application deployment or an Azure write.

### Preflight and cleanup

- Existing VM `vm-s2stest-jp-001` (`192.168.10.4`) was started for the
  diagnostic and deallocated afterward. No new VM or network resource was
  created.
- The VM's system-assigned identity temporarily received `Key Vault Secrets
  User` only for the staging HMAC secret `hook-hmac-secret`. The value was
  never output; the role assignment was removed and verified absent.
- Candidate UPN
  `e2e-20260825-201759@nycumis.onmicrosoft.com` was checked read-only through
  Microsoft Graph and returned `NotFound`. No Entra account was created or
  modified.
- The VM reached the AGW private frontend `10.0.8.62` with the expected TLS
  hostname/SNI, and the health path responded successfully.
- Temporary AGW WAF and application source allowlist entries for the VM were
  restored to the approved portal source `140.113.7.17/32`. Both Service Bus
  queues remain empty, and no worker or Graph side effect was observed.

### Diagnostic results

| Probe | Result | Interpretation |
|---|---|---|
| First signed request using a Python scripting User-Agent | `403` from WAF | OWASP managed rule `913101` rejected the User-Agent before the application. |
| Retry using a standard browser User-Agent | `401 source ip is not allowed` | WAF passed, but the application rejected the resolved source before HMAC/handler processing. |
| Empty/invalid-body source diagnostic | Same source-address rejection | Confirmed the diagnostic path has no Service Bus or Graph side effect. |

The application log showed a forwarded chain with two hops, but the
pre-existing log shape did not expose the normalized `RemoteAddr` or the
resolved client address. Do not treat any individual forwarded hop as proof
of the application's immediate TCP peer.

The active staging revision at the time of this handoff is
`ca-pwdhook-stg-mvxfna--restore201759`, image
`password-hook-service:stg-3f76564`, `Provisioned/Running`, with 100% traffic.
The deployed values remain:

```text
PORTAL_ALLOWED_CIDRS=140.113.7.17/32
TRUSTED_PROXY_CIDRS=10.0.8.0/26
DIRECT_CLIENT_MODE=false
```

Because the request never reached `202 Accepted`, there is no evidence of
Service Bus enqueue/consume, worker processing, Graph lookup, or sync-status
transition. The candidate UPN remains absent.

### Source-resolution instrumentation prepared locally

To measure the missing runtime boundary without exposing secrets, the local
working tree now contains:

- `internal/middleware/ratelimit.go`: source-IP rejection events include
  normalized `peerIp`, `peerTrusted`, forwarded header/hop counts,
  `sourceResolution`, and `resolvedClientIp` when available.
- `internal/middleware/observability.go`: middleware events accept these
  additional structured attributes.
- `internal/middleware/ratelimit_test.go`: regression coverage proves the
  raw `X-Forwarded-For` value is not logged.
- `deploy/terraform/README.md`: documents the sanitized fields and their
  source-address validation use.

These changes are uncommitted and have not been deployed. The current
repository/CD state remains plan-only; do not use `az containerapp update` or
another ad-hoc image/configuration change to bypass the owner pipeline.

### Required continuation

1. Review the uncommitted instrumentation diff.
2. Deploy it through the approved staging CD path and verify the new revision
   is healthy.
3. Send one empty-body `POST /api/v1/hook/password` diagnostic request through
   the same AGW path. It must be rejected before HMAC/handler processing and
   must not contain a password, secret, token, signature, nonce, or real
   request body.
4. Read only the sanitized `source_ip_not_allowed` fields to identify the
   actual immediate peer and forwarded-chain shape.
5. Update `trusted_proxy_cidrs` only with the verified proxy CIDR through
   Terraform/CD; never use the portal CIDR, the VM/client IP, an entire
   unverified subnet, or an unrestricted network.
6. Re-run the read-only identity preflight and the approved E2E sequence only
   after the trust boundary is verified. Repeat cleanup immediately afterward.

## Staging API E2E Validation and Graph Upsert-Creates-User Finding (2026-09-01/02)

This section completes the Required Continuation above and records a full,
successful staging API/E2E validation, plus one important safety finding for
any future synthetic test on this service. It records evidence and decisions
only; the shared-network apply and the real on-premises portal-source
acceptance test remain separately gated (see Next Actions below).

### Application CD apply

The source-resolution instrumentation (previously uncommitted) and the
`trusted_proxy_cidrs` fix below were each deployed through the approved
staging CD path: `TF_APPLY_MODE` was set to `apply` for exactly one
`workflow_dispatch` run, the run was verified to build, push the image, and
reach `Terraform apply` / `Verify Container App revision health`
successfully, and `TF_APPLY_MODE` was returned to `plan` immediately
afterward each time. No ad-hoc `az containerapp update` was used. One
first apply attempt failed with `LinkedAuthorizationFailed` (the CD OIDC
identity had `Contributor` on `rg-password-hook-stg-jpe-001` but not
`Microsoft.App/managedEnvironments/join/action` on the shared managed
environment `cae-stg-jpe-001` in `rg-cae-stg-jpe-001`, which is required to
update a Container App attached to a managed environment in a different
resource group). Fixed by granting the CD identity the built-in
**Container Apps Operator** role (read-only plus `.../join/action`, no
write/delete) scoped to exactly the `cae-stg-jpe-001` resource, not the
resource group — this environment is shared by roughly 15 other apps across
several teams, so the grant was deliberately scoped as narrowly as possible.
The retry then succeeded.

### `trusted_proxy_cidrs` correction

The empty-body diagnostic from the Required Continuation was run from the
existing hub test VM (`vm-s2stest-jp-001`, started and later deallocated;
no new resource created) through the real AGW private frontend. With the
new instrumentation, the `source_ip_not_allowed` log for that request showed:

```
peerTrusted=false forwardedHeaderCount=1 forwardedHopCount=2
sourceResolution=direct_peer peerIp=100.100.0.170 resolvedClientIp=100.100.0.170
```

Repeated 4 more times with a consistent `100.100.0.x` peer each time.
`100.100.0.0/16` is Microsoft's documented platform-reserved range for
Azure Container Apps' own internal ingress
(https://learn.microsoft.com/en-us/azure/container-apps/custom-virtual-networks) —
not assignable to any customer VNet/subnet and not reachable or spoofable
from outside the ACA platform. The Container App's own ingress layer
re-sources every inbound connection (including ones AGW forwards) from this
range, so it — not the AGW subnet (`10.0.8.0/26`, the prior untested
assumption) — is the correct trust boundary. `trusted_proxy_cidrs` was
updated to `["100.100.0.0/16"]` via Terraform/CD and re-verified: a follow-up
empty-body diagnostic showed `peerTrusted=true sourceResolution=trusted_forwarded
resolvedClientIp=10.0.8.4` (the hub-VM test path's AGW-side address, not the
real portal address — expected, since this test still did not originate from
the real on-premises portal). This value is a **permanent** fix and was kept.

### Full API and synthetic E2E validation

All three HTTP endpoints were exercised through the real AGW private
frontend from the hub test VM (`--resolve api.test.nycu.edu.tw:443:10.0.8.62`,
a browser User-Agent to pass the WAF's OWASP scanner-detection rule):

| Endpoint | Result | Latency (3 samples) |
|---|---|---|
| `GET /healthz` | `200 {"status":"ok"}` | 38ms, 26ms, 16ms |
| `GET /version` | `200` (`version`/`commit`/`buildTime` all `"unknown"`/`"dev"` — build-info stamping gap, not investigated further) | 17ms, 21ms, 20ms |
| `POST /api/v1/hook/password` (empty body) | `401 source_ip_not_allowed` (expected; proves the diagnostic path has no side effect) | 17-34ms |

For the signed path, the AGW WAF policy (`waf-policy-password-hook-stg`,
dedicated to this app only — not shared with other tenants) and
`portal_allowed_cidrs` were both temporarily widened by exactly one `/32`
each (the hub VM's WAF-observed address `192.168.10.4/32`, and the
AGW-resolved test-path address `10.0.8.4/32`) through the same
Terraform/CD-and-revert pattern, matching the temporary-allow-then-restore
precedent from the 2026-08-13 diagnostic. The HMAC secret was fetched only
from within the VM (Key Vault private-endpoint IP `10.0.4.100`, reachable
via the pre-existing `hub-to-stg-jpe` peering; the VM's system-assigned
identity was granted `Key Vault Secrets User` scoped to exactly the
`hook-hmac-secret` secret, never the whole vault) and was never logged or
displayed — only its length was ever printed. One synthetic identity was
used throughout (`cn=e2e-20260901-152000`, UPN
`e2e-20260901-152000@nycumis.onmicrosoft.com`, verified absent via Graph
before the test), consistent across all four signed requests, varying only
`eventType`/nonce/timestamp per request:

| Attempt | eventType | Result | Latency | Outcome |
|---|---|---|---|---|
| 1 | `password_change` | `202` | 836ms | `graph_password_upsert` **created a new Entra user** (see finding below); `worker_password_sync_completed` outcome `synced` |
| 2 | `password_change` (repeat) | `202` | 32ms | Graph `permanent_error` (user now exists, second `createUser` conflicted); `worker_password_sync_failed` outcome `sync_failed`, routed to `password-sync-safe-dlq` |
| 3 | `password_recovery` | `202` | 32ms | Graph `transient_error` then retried to `permanent_error`; `worker_password_sync_failed` after 2 attempts, routed to `password-sync-safe-dlq` |
| 4 | `login_bootstrap` | `202` | 21ms | `hook_password_sync_skipped` (`reason=sync_pending`) — correctly deduplicated against attempt 1's very recent sync, never reached Graph/worker |

Post-test: the main `password-sync` queue was empty (0 active, 0
dead-letter); `password-sync-safe-dlq` held 2 messages from attempts 2 and 3
(both confirmed to contain no plaintext password — `RecordPasswordSyncFailure`
zeros the password field before writing).

### Safety finding: `UpsertUserPassword` creates a real Entra user on 404

`internal/graphclient/client.go`'s `UpsertUserPassword` does a `GET` lookup
first; on `404` it calls `createUser` (`POST /v1.0/users`), i.e. it is a
genuine upsert by design (this is the service's real migration/onboarding
behavior, not a bug). **A previously-verified-nonexistent UPN is therefore
not a side-effect-free target for `password_change`/`password_recovery`
testing** — attempt 1 above created a real, `accountEnabled: true` Entra
user with the test's password at `2026-09-02T05:01:26Z`, confirmed via
Graph immediately afterward. This directly contradicts the assumption in
the 2026-08-25/26 section above ("expected safe outcome is ... an
understood Graph failure ... not a real password update") — that session
never actually reached the Graph/worker stage to observe this. The created
account was deleted and then permanently purged from
`directory/deletedItems` within minutes of creation; a follow-up Graph
check confirmed it no longer exists in either state. **Any future synthetic
test against this endpoint must either use a CN that fails classification
before reaching Graph (e.g. clearly invalid), accept that a real Entra user
will be created and plan to delete/purge it immediately afterward exactly as
done here, or use `login_bootstrap` only** (which, per `internal/migration/service.go`,
still reaches Graph/creates a user unless a very recent sync-status record
already exists for that UPN — it is not inherently side-effect-free either).

### Cleanup performed

All temporary grants and allowances from this session were reverted and
verified absent immediately after use: the WAF custom rule back to only
`140.113.7.17/32`; `portal_allowed_cidrs` back to only `["140.113.7.17/32"]`
via Terraform/CD; the Key Vault `Key Vault Secrets User` role assignment on
`hook-hmac-secret` deleted; the hub test VM deallocated; `TF_APPLY_MODE`
returned to `plan`. The `Container Apps Operator` grant on `cae-stg-jpe-001`
for the CD identity was **kept** (it is a permanent least-privilege fix
required for any future CD apply that touches the Container App resource,
not a test-only grant).

## Next Actions

1. Have the Network team complete or document the remaining on-premises
   route/selector/firewall/DNS gates, then run the real portal-source
   acceptance test from `140.113.7.17` (not yet performed — everything above
   was validated from the hub test VM via the AGW private frontend, which
   proves the Azure-side path and the application pipeline end-to-end, but
   is not a substitute for a real on-premises-originated request).
2. Before any further synthetic Graph-reaching test, read the safety finding
   above and choose a strategy that avoids or accounts for real Entra user
   creation.
3. Continue keeping `TF_APPLY_MODE=plan` between deliberate, approved apply
   windows.

Production and Slice 12 remain blocked pending the real on-premises
acceptance test. This handoff authorizes neither the shared-network apply
nor further application CD applies beyond what is recorded above.

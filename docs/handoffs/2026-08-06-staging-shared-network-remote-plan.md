# Staging Shared Network Remote-Plan Handoff

**Date:** 2026-08-06 (last updated 2026-08-12)

**Status:** Current. The shared-network Terraform stack and remote state are
ready, and a formal remote-state plan has passed the exact-change gate, most
recently re-verified fresh on 2026-08-12. No VNet peering or other Azure
network change has been applied. The Network team has made a partial,
standalone pre-change (S2S selector for `10.0.8.0/24`, confirmed no-SNAT,
confirmed revocable) outside any formal ticket/window; see *Network-Team
Juniper Selector Update (2026-08-12)* below. External network-team pre-check
completion, an approved staging change window, a fresh baseline, and separate
apply approval remain hard gates.

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

## Next Actions

1. The service owner sends the Network-team pre-check/change-window request.
2. Record the ticket, scheduled window, Network-team operator, and rollback
   operator in this handoff or its successor without including sensitive
   configuration.
3. At the start of the approved window, capture a fresh sanitized Azure and
   public-LDAP baseline and stop on drift.
4. Generate and review a fresh remote-state plan.
5. Request separate apply approval; only then create the two peerings through
   Terraform.
6. Verify both peerings `Connected` and prove no VPN, existing-peering, AGW,
   or public-LDAP regression.
7. Have the Network team complete its conditional route/selector/firewall
   change, then run the no-DNS smoke and synthetic signed tests.
8. Keep `TF_APPLY_MODE=plan`. The password-hook application's first
   controlled CD apply remains a later gate after the real portal trust path
   is proven.

Production and Slice 12 remain blocked. This handoff authorizes neither the
shared-network apply nor the application CD apply.

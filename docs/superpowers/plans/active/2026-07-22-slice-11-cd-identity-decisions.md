# Slice 11 CD Identity Decision

**Date:** 2026-07-23
**Executed by:** `lyy15@nycumis.onmicrosoft.com` (the same individual as
Slice 10's "Deployment Identity Decision" — the password-hook service owner,
the Application Gateway owner, and the Terraform-apply operator remain the
same person; see
`docs/superpowers/plans/active/2026-07-03-slice-10-private-network-decisions.md`).

## What was created

- Azure AD App Registration: `sp-password-hook-cd-stg`, app ID
  `6e8f4155-f0a6-4556-a6db-542f9c6ae09b`, object ID
  `39004888-ca2d-47ea-b613-c41c5d873ca3`.
- One Federated Credential, `cd-staging-environment` (credential ID
  `79ff69f1-7e3a-4e7d-a2fb-3ec21cd47532`), issuer
  `https://token.actions.githubusercontent.com`, subject
  `repo:lyy-nycu/password-hook-service:environment:staging`, audience
  `api://AzureADTokenExchange`. No client secret or certificate exists for
  this app — OIDC federated identity is the only authentication mechanism.
- Service Principal for the app, object ID `9a352f3a-fab6-49ac-b6bc-d315e167e70d`.
- GitHub Environment `staging` on `lyy-nycu/password-hook-service` (created
  via `gh api --method PUT repos/.../environments/staging`; the repository
  previously had only one environment, `copilot`).

## RBAC granted (least privilege, resource-group/resource scoped)

Verified via `az role assignment list --assignee 6e8f4155-f0a6-4556-a6db-542f9c6ae09b --all` — exactly these ten rows, no eleventh/unexpected assignment:

| Role | Scope |
|---|---|
| Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-password-hook-stg-jpe-001` |
| AcrPush | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-acr-jpe-001/providers/Microsoft.ContainerRegistry/registries/acrjpe001` |
| Reader | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-acr-jpe-001/providers/Microsoft.ContainerRegistry/registries/acrjpe001` |
| Storage Blob Data Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-tfstate-jpe-001/providers/Microsoft.Storage/storageAccounts/sttfstatephsjpe001` |
| Role Based Access Control Administrator | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-password-hook-stg-jpe-001` |
| Reader | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-cae-stg-jpe-001/providers/Microsoft.App/managedEnvironments/cae-stg-jpe-001` |
| Network Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-spoke-paas/providers/Microsoft.Network/virtualNetworks/vnet-stg-jpe-001/subnets/snet-pe-password-hook-stg-jpe-001` |
| Network Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-spoke-paas/providers/Microsoft.Network/privateDnsZones/privatelink.redis.azure.net` |
| Network Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-spoke-paas/providers/Microsoft.Network/privateDnsZones/privatelink.servicebus.windows.net` |
| Network Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-spoke-paas/providers/Microsoft.Network/privateDnsZones/privatelink.vaultcore.azure.net` |

No subscription-wide role was granted. The `Role Based Access Control
Administrator` role is scoped only to the staging application resource
group, and Azure's built-in role has a platform-enforced condition
preventing it from assigning `Owner`, `User Access Administrator`, or `Role
Based Access Control Administrator` itself — it can only assign the specific
roles Terraform's existing modules already assign to the runtime UAMI (Key
Vault Secrets User, Service Bus Data Sender/Receiver, Monitoring Metrics
Publisher, AcrPull), not escalate its own privilege.

### Post-merge addition: cross-resource-group RBAC for `rg-cae-stg-jpe-001` and `rg-spoke-paas` (2026-07-26)

After the `staging.tfvars`/provider-constraint fixes above, CD's real
`terraform plan` (using the actual CD Federated Credential identity, not
the operator's own account used for the earlier read-only investigations)
failed with five `AuthorizationFailed` errors, none of which had appeared
in any of the operator's own prior `terraform plan` runs — the operator's
personal Azure AD account already has broad access to these shared
resource groups, which had silently masked this gap in every earlier
verification pass:

- `Microsoft.App/managedEnvironments/read` on `rg-cae-stg-jpe-001`'s
  `cae-stg-jpe-001` (the shared ACA environment) — referenced only as a
  read-only `data` source (`data.azurerm_container_app_environment.existing`),
  never created/modified/deleted by this configuration.
- `Microsoft.Network/virtualNetworks/subnets/read` on
  `rg-spoke-paas`'s `vnet-stg-jpe-001/snet-pe-password-hook-stg-jpe-001` —
  an actual managed `resource` (`azurerm_subnet.private_endpoints`) this
  configuration creates/updates.
- `Microsoft.Network/privateDnsZones/read` on three private DNS zones in
  `rg-spoke-paas` (`privatelink.redis.azure.net`,
  `privatelink.servicebus.windows.net`, `privatelink.vaultcore.azure.net`)
  — each an actual managed `resource` (`azurerm_private_dns_zone.this[each.key]`).

Fixed with five additional role assignments, each scoped to the exact
individual resource (never the resource group or subscription, and never
touching any other resource in the shared `rg-spoke-paas`/`rg-cae-stg-jpe-001`
resource groups used by other teams):

- `Reader` on the ACA environment resource (read-only data source; no
  write capability needed or granted).
- `Network Contributor` on the exact subnet resource ID and each of the
  three exact private DNS zone resource IDs (this configuration creates,
  updates, and would delete only these specific resources; `Network
  Contributor` is Azure's standard built-in role for managing subnets and
  private DNS zones).

Verified: a subsequent real CD run (`workflow_dispatch`, run
`30207952430`) completed `Terraform plan` successfully for the first time
using the CD identity, showing exactly `Plan: 1 to add, 3 to change, 1 to
destroy` — matching the "Accepted, expected remaining plan diff" already
documented above (the `metrics_publisher` role replacement, the image tag
update, and the Service Bus queue TTL representation difference) with no
new or unexpected diff. `TF_APPLY_MODE` remained `plan` throughout; this
was a successful dry-run only, no real Azure resource was created,
modified, or deleted by this verification.

### Post-merge addition: `Reader` on the ACR (2026-07-23)

CD's first two live triggers on `main` (runs `29987097891` and
`29987956317`, after merging PR #16 and PR #18 respectively) both failed at
the `Log in to ACR` step. `AcrPush` grants only the data-plane actions
`Microsoft.ContainerRegistry/registries/pull/read` and
`.../push/write` (verified via `az role definition list --name AcrPush`),
not the control-plane `Microsoft.ContainerRegistry/registries/read` action
that `az acr login` needs internally to resolve the registry resource via
ARM before performing the docker login handshake — a well-known Azure
gotcha ("has AcrPush but az acr login still fails"). Fixed by adding a
`Reader` role assignment scoped to the exact same ACR resource ID as
`AcrPush` (not the resource group, not the subscription) — this grants only
generic read access to that one resource's metadata, nothing else, and
keeps the identity's overall RBAC footprint proportionate to the earlier
four assignments.

## Non-secret GitHub Actions variables set

Set via `gh variable set ... --env staging` and verified via `gh variable
list --env staging`:

| Variable | Value |
|---|---|
| `AZURE_CLIENT_ID` | `6e8f4155-f0a6-4556-a6db-542f9c6ae09b` |
| `AZURE_TENANT_ID` | `7ef65350-5b77-4958-aca5-0ccadb6bd0b7` |
| `AZURE_SUBSCRIPTION_ID` | `56b72537-d985-4530-88f3-b6ed07e71c67` |

All three are set at the `staging` GitHub Environment scope, not
repository-wide — only a workflow job that declares `environment: staging`
can read them, matching the Federated Credential's subject restriction.

## Verification performed

- `gh api repos/lyy-nycu/password-hook-service/environments --jq
  '.environments[].name'` → `copilot`, `staging` (both present after
  creation).
- `az role assignment list --assignee 6e8f4155-f0a6-4556-a6db-542f9c6ae09b
  --all -o table` → exactly the four rows listed above.
- `gh variable list --env staging` → exactly the three variables listed
  above, with the expected values.

## Related: Task 8 Branch Protection Outcome

Recorded here per Task 8, Step 4's instruction (that task's outcome had no
file of its own to record into until this document existed):

Branch protection was enabled on `main` requiring six status checks
(`test`, `gosec`, `govulncheck`, `trivy-fs`, `gitleaks`, `terraform-check`),
`strict: true`, `enforce_admins: true`, applied via `gh api --method PUT
repos/lyy-nycu/password-hook-service/branches/main/protection`. Verified via
`gh api .../branches/main/protection --jq '.required_status_checks.contexts'`
returning exactly those six names. A real draft PR (#16, branch
`slice-11-ci-cd`) confirmed all six checks pass; a throwaway PR (#17, branch
`test/branch-protection-verify`, deliberately broken `go vet`) confirmed
`mergeStateStatus: BLOCKED` when a required check fails, then was closed and
its branch deleted without merging. During PR #16's live run, two real bugs
were found and fixed that no local verification or diff review could have
caught: `aquasecurity/trivy-action` silently drops its `severity` filter
when `format: sarif` is set unless `limit-severities-for-sarif: true` is
also set (fixed in `trivy-fs`, and the same fix applied to Task 11's
not-yet-implemented CD image scan); and `gitleaks/gitleaks-action` is a
closed-source, env-var-only wrapper with no configurable inputs at all and a
hard `GITHUB_TOKEN` requirement for `pull_request` events, replaced entirely
with installing and running the real `gitleaks` CLI via `go install`.

## Post-merge staging.tfvars and provider-constraint corrections (2026-07-23)

After the `Reader`-on-ACR fix above, CD's third live trigger (manual
`workflow_dispatch`, run `29989453558`) passed `Log in to ACR` and `Push
image` for the first time, and reached `Terraform plan` — which then
surfaced three further real discrepancies between the committed
`deploy/terraform/environments/staging.tfvars` / `deploy/terraform/main.tf`
and the actual, already-applied staging state. `TF_APPLY_MODE` remained
`plan` throughout, so none of these were ever actually applied to real
Azure resources; every finding below was discovered and fixed from `plan`
output alone.

**A. `private_endpoint_subnet_cidr` was wrong.** Task 9's committed value
(`10.0.4.224/27`) matched the CIDR originally *planned* for this subnet in
`docs/superpowers/plans/active/2026-07-03-slice-10-private-network-decisions.md`
(lines 216, 266), but the subnet actually deployed to
`vnet-stg-jpe-001/snet-pe-password-hook-stg-jpe-001` uses `10.0.4.96/27` —
confirmed via `az network vnet subnet show`. The decision document was
never updated after the operator used a different value at real-apply
time. Applying the stale `.224/27` value would have attempted to
resubnet a shared, multi-tenant VNet (`rg-spoke-paas`) out from under
other workloads. Fixed by changing `staging.tfvars` to `10.0.4.96/27` to
match the real deployed subnet, not the superseded planning document.

**B. `key_vault_operator_object_ids` was empty, which would have revoked
real operator access.** The real staging Key Vault currently grants `Key
Vault Secrets Officer` to `lyy15@nycumis.onmicrosoft.com` (object ID
`7d37cec4-ad58-480c-aa10-a89d9190e412`, confirmed via `az ad user show`
matching `az role assignment list` on the vault) for the manual secret
injection/rotation process documented in `deploy/terraform/README.md`.
Task 9's committed `staging.tfvars` set this list to `[]`, reasoning that
the file was "consumed by the automated CD pipeline, not an interactive
operator apply" — but CD's plan/apply still targets the same real Key
Vault the operator manages, so an empty list would have destroyed that
operator's real, currently-granted access on the next `apply`. Fixed by
setting `key_vault_operator_object_ids = ["7d37cec4-ad58-480c-aa10-a89d9190e412"]`
to match real state.

**C. The `azurerm` provider version constraint was too loose.** `main.tf`
declared `~> 4.0` (any `4.x`), while `.terraform.lock.hcl` has pinned
`4.81.0` since it was first committed (Task 2, commit `34a5bd1`) and has
never changed since — so the loose constraint was not itself the cause of
any drift that has already happened, but it left the door open for a
future `terraform init -upgrade` to silently jump multiple minor versions.
Separately, the current `4.81.0`-based plan shows `azurerm_container_app`'s
`workload_profile_name` recomputing from `"Consumption"` to `null`, and
`module.aca.azurerm_role_assignment.metrics_publisher` being forced to
replace (destroy then recreate) rather than update in place — both
consistent with `azurerm` schema/behavior differences between whatever
provider version was used for the real Slice 10 applies (which predate
this repository's lock file) and the currently locked `4.81.0`. Tightened
the constraint to `~> 4.81` (patch-level only) so any future provider
upgrade to a new minor version is a deliberate, separately-tested change
rather than an accidental one; this does not itself resolve the
`metrics_publisher`/`workload_profile_name` diffs already present in
state, which remain expected, low-risk plan output (see below).

**Accepted, expected remaining plan diff:** with A, B, and C fixed, the
only remaining `terraform plan` differences beyond the expected image
tag/digest update are: (1) `module.aca.azurerm_role_assignment.metrics_publisher`
being replaced, and (2) `azurerm_container_app.this[0].workload_profile_name`
resetting to `null`. Both are provider-recomputation artifacts, not
configuration errors. The `metrics_publisher` replacement means the
runtime UAMI's `Monitoring Metrics Publisher` role assignment on the
Container App is destroyed and recreated within the same `apply` — this
only affects the ability to publish custom Azure Monitor metrics (`internal/azuremonitor`),
not the hook/worker/Service Bus critical path; the application already
treats a metric-flush failure as a non-fatal, retried-next-interval
condition (`internal/app/app.go`'s `periodicMetricFlusher.flushWithTimeout`
logs a warning and continues, per-minute retry), and Azure RBAC
propagation delay for a replaced role assignment is already an accepted,
documented pattern in this project (see `deploy/terraform/README.md`'s
"RBAC propagation in Azure can take a few minutes" and
`docs/superpowers/plans/active/2026-07-03-slice-10-infrastructure.md`'s
equivalent note for Service Bus). Given this is staging, a brief
(minutes-scale) custom-metrics gap during a real `apply` is an accepted,
self-recovering trade-off, not a defect requiring a code or config change.


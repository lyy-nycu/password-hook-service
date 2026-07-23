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

Verified via `az role assignment list --assignee 6e8f4155-f0a6-4556-a6db-542f9c6ae09b --all` — exactly these four rows, no fifth/unexpected assignment:

| Role | Scope |
|---|---|
| Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-password-hook-stg-jpe-001` |
| AcrPush | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-acr-jpe-001/providers/Microsoft.ContainerRegistry/registries/acrjpe001` |
| Storage Blob Data Contributor | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-tfstate-jpe-001/providers/Microsoft.Storage/storageAccounts/sttfstatephsjpe001` |
| Role Based Access Control Administrator | `/subscriptions/56b72537-d985-4530-88f3-b6ed07e71c67/resourceGroups/rg-password-hook-stg-jpe-001` |

No subscription-wide role was granted. The `Role Based Access Control
Administrator` role is scoped only to the staging application resource
group, and Azure's built-in role has a platform-enforced condition
preventing it from assigning `Owner`, `User Access Administrator`, or `Role
Based Access Control Administrator` itself — it can only assign the specific
roles Terraform's existing modules already assign to the runtime UAMI (Key
Vault Secrets User, Service Bus Data Sender/Receiver, Monitoring Metrics
Publisher, AcrPull), not escalate its own privilege.

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


# Slice 11 CI/CD Design

> **Status:** Approved via brainstorming session; not yet implemented.
>
> **Source:** `docs/2026-07-21-slice-10-to-slice-11-handoff.md`, `docs/superpowers/plans/roadmap.md` (Slice 11 row), `docs/superpowers/specs/2026-06-24-password-hook-service-design.md` (§10 CI/CD Pipeline).

## 1. Goal

Match `docs/superpowers/plans/roadmap.md`'s Slice 11 done criteria: "CI runs
tests, vet, gosec, govulncheck, trivy, and gitleaks; CD builds image and
supports staging deployment." Slice 11 depends only on "Infrastructure shape,"
which the merged Slice 10 Terraform already satisfies — it does not require
Slice 10's live on-premises/production validation gates to be closed first.

## 2. In Scope

- CI: `go test`, `go vet` (existing), plus `gosec`, `govulncheck`, `trivy fs`,
  `gitleaks`, a report-only test coverage summary, and a conditional
  `terraform fmt -check` / `validate` job.
- SARIF upload of `gosec`/`trivy fs`/`gitleaks` results to GitHub Code
  Scanning (the repository is public, so this is free, no GHAS license
  required).
- GitHub branch protection on `main`: require all CI jobs to pass before
  merge (currently **unset** — verified via `gh api repos/.../branches/main/protection` → `404 Branch not protected`).
- CD: on push to `main`, build the container image, scan it with `trivy
  image`, authenticate to Azure via OIDC (no stored client secret), push to
  the existing ACR, and run Terraform against the real staging environment
  behind a plan/apply safety valve (see §7.4), then verify the resulting
  Container App revision is healthy via the Azure API.
- A dedicated Azure AD App Registration + Federated Credential for CD,
  created during implementation using the current session's authenticated
  Azure identity, with least-privilege RBAC, and the decision recorded (same
  pattern as Slice 10's "Deployment Identity Decision").
- A committed `deploy/terraform/environments/staging.tfvars` containing the
  real, non-secret staging resource identifiers already recorded in the
  Slice 10 handoff/decision documents.
- Documentation: update `deploy/terraform/README.md` (and/or root `README.md`)
  to explain the CD pipeline, the plan/apply safety-valve switch, and how to
  operate it, so other developers/agents don't have to rediscover it from the
  workflow YAML.
- Recording "enable GitHub Copilot Agentic Autofix for code scanning alerts"
  as an implementation checklist item (a repo/org setting, not new pipeline
  code), since it becomes usable once SARIF upload is in place.

## 3. Out of Scope / Follow-up

These are explicitly **not** part of Slice 11. They are recorded here so they
are not lost, but no separate draft plan file or roadmap entry is created for
them yet:

- **Production `terraform apply`.** Slice 10's on-premises/production
  validation gates (real portal traffic, split-horizon DNS from a real
  on-prem host, the S2S VPN path itself) are not yet closed. Roadmap's
  **Slice 12 ("Integration and Production Readiness")** already owns
  production rollout once Slices 1–11 are done; production CD belongs there,
  not here.
- **Scheduled scans** (daily `govulncheck` + `trivy` on `main`) and **weekly
  OWASP ZAP DAST against staging**, both mentioned in the design spec's
  §10.3 but not present anywhere in `roadmap.md`'s slice list.
- **A real, API-hitting smoke test in CD** (e.g., `GET /healthz` or a signed
  hook request against the live staging endpoint). The shared ACA environment
  is internal-only (`vnetConfiguration.internal = true`, no public inbound
  IP), so a GitHub-hosted runner cannot reach it. Running ZAP DAST or a real
  smoke test would require standing up new infrastructure with VNet
  connectivity (e.g., a self-hosted GitHub Actions runner placed in
  `vnet-stg-jpe-001`, or making the temporary `vm-client` peering pattern from
  the Slice 10 validation permanent) — a non-trivial new infrastructure
  decision that deserves its own future brainstorming session, not a default
  bundled into Slice 11.

## 4. Architecture Overview

Two GitHub Actions workflows, both event-triggered (no polling, no
`workflow_run` chaining):

```
pull_request / push(main)
        │
        ▼
   .github/workflows/ci.yml
   ├── test              (go test + go vet + coverage summary)
   ├── gosec             ─┐
   ├── govulncheck        │ parallel, independent jobs
   ├── trivy-fs           │ (no `needs:` between them)
   ├── gitleaks          ─┘
   └── terraform-check   (always runs; skips its checks internally if
                           deploy/terraform/** unchanged, so the required
                           status check always reports)

push(main) only, after branch protection already required the above to pass
        │
        ▼
   .github/workflows/cd.yml
   1. build image (tag: stg-<short-sha>)
   2. trivy image scan (severity CRITICAL,HIGH, exit-code 1)
   3. azure/login (OIDC, federated credential)
   4. push image to existing ACR (acrjpe001)
   5. terraform init (staging backend-config)
   6. terraform plan|apply (mode controlled by TF_APPLY_MODE, see §7.4)
        -var-file=deploy/terraform/environments/staging.tfvars
        -var app_image=... -var app_image_tag=<short-sha>
        -var deploy_container_app=true
   7. verify Container App revision provisioningState/healthState via
      `az containerapp revision show`
```

## 5. CI Workflow (`ci.yml`)

### 5.1 `test` job (existing, extended)

- `go test ./... -coverprofile=coverage.out`
- `go vet ./...`
- `go tool cover -func=coverage.out` → appended to `$GITHUB_STEP_SUMMARY`.
  Report-only: no minimum-coverage threshold is enforced. (Current coverage
  spans ~40%–100% across packages, verified via a local run; the low outlier
  is `internal/httpserver` at 40%. Adding a hard gate now would immediately
  fail on pre-existing code and requires a separate baseline decision, so it
  is deferred.)

### 5.2 Security scan jobs (new, parallel)

Four independent jobs, each its own required status check:

| Job | Tool | Failure condition | SARIF |
|---|---|---|---|
| `gosec` | `gosec ./...` | `-severity medium` (reports/fails on Medium+High; gosec has no "Critical" level) | yes |
| `govulncheck` | `govulncheck ./...` | any known vulnerability reachable from the call graph | no (no official SARIF output; plain log/summary) |
| `trivy-fs` | `aquasecurity/trivy-action`, `scan-type: fs` | `severity: CRITICAL,HIGH`, `exit-code: 1` | yes |
| `gitleaks` | `gitleaks/gitleaks-action` | any detected secret pattern (no severity concept) | yes |

SARIF-producing jobs upload via `github/codeql-action/upload-sarif`, giving
persistent findings in the repo's Security → Code scanning alerts tab and
inline PR annotations, in addition to failing the job (which blocks merge via
branch protection).

### 5.3 `terraform-check` job (new)

Runs in the same always-triggered `ci.yml` (not a separate path-filtered
workflow file — a separate workflow with `paths: ['deploy/terraform/**']`
would never produce a status check on PRs that don't touch Terraform, which
would make branch protection block those PRs forever). Instead:

1. Detect whether `deploy/terraform/**` changed (e.g. via `dorny/paths-filter`).
2. If unchanged, the job still completes (reporting a pass) without running
   Terraform steps.
3. If changed: `terraform -chdir=deploy/terraform fmt -check -recursive`,
   `terraform -chdir=deploy/terraform init -backend=false`, `terraform
   -chdir=deploy/terraform validate`. No cloud login needed.

The exact conditional syntax will be verified against the current Actions
runner/action versions during plan-writing and implementation, not guessed
here.

## 6. Branch Protection

Require these status checks on `main` before merge: `test`, `gosec`,
`govulncheck`, `trivy-fs`, `gitleaks`, `terraform-check`. `main` currently has
no protection at all (`404 Branch not protected` per live check on
2026-07-21); this task creates it for the first time.

## 7. CD Workflow (`cd.yml`)

### 7.1 Trigger

`push: branches: [main]`, replacing the current `workflow_dispatch`-only
trigger. Keep `workflow_dispatch` as an additional manual trigger for re-runs.

### 7.2 Build, scan, push

1. Checkout, `docker build -f deploy/Dockerfile -t <tag> .` where `<tag>` is
   `stg-<short-sha>` (matching the existing image-tag convention observed in
   the handoff, e.g. `stg-3f76564`).
2. `trivy image` scan of the built image: `severity: CRITICAL,HIGH`,
   `exit-code: 1`.
3. `azure/login@v2` using OIDC (client-id/tenant-id/subscription-id stored as
   non-secret GitHub repository/environment variables — federated credential
   trust means no client secret is ever stored).
4. Authenticate to and push the image to the existing ACR (`acrjpe001`).

### 7.3 Terraform

`terraform init` with `-backend-config` for the staging state key (per
`deploy/terraform/README.md`'s documented backend), then plan or apply (see
§7.4) using:

```
-var-file=deploy/terraform/environments/staging.tfvars
-var app_image=<acr-login-server>/password-hook-service:<short-sha>
-var app_image_tag=<short-sha>
-var deploy_container_app=true
```

`staging.tfvars` holds the real, non-secret staging resource identifiers
already recorded in
`docs/superpowers/plans/active/2026-07-03-slice-10-private-network-decisions.md`
and the Slice 10→11 handoff (resource group name, existing ACR/ACA
environment IDs, network/private-endpoint inputs, etc.) — everything except
`app_image`/`app_image_tag`, which the workflow supplies dynamically per run
since they change every deploy.

### 7.4 Plan/apply safety valve

Because this is a brand-new, never-exercised automation with write access to
real, already-live Azure staging resources (`rg-password-hook-stg-jpe-001`
and everything in it), the workflow's Terraform step is gated by a
repository variable, e.g. `TF_APPLY_MODE`:

- `plan` (**initial default**): run `terraform plan` only. This computes and
  prints the changes Terraform *would* make without touching any real Azure
  resource. Since staging's live state should already match the repository
  configuration (Slice 10 applied these same values manually), the expected
  output is "no changes" — any unexpected diff is a signal to fix the
  automation before trusting it with `apply`.
- `apply`: after a human reviews at least one clean `plan` run, they flip
  this variable and subsequent merges to `main` run `terraform apply
  -auto-approve` for real.

This must be documented (see §9) so the switch-over isn't a tribal-knowledge
step.

### 7.5 Post-apply verification

Query the Container App revision via `az containerapp revision show` (or
`list`) and fail the job unless `provisioningState`/`runningState` indicate a
healthy, running revision. No API-hitting smoke test (see §3) — this is an
Azure-control-plane-level health check only.

### 7.6 Concurrency and failure handling

- `concurrency: group: cd-staging, cancel-in-progress: false` so overlapping
  merges queue rather than run two simultaneous `terraform apply`s against
  the same state (Azure's blob-lease state lock would otherwise force one run
  to fail on lock contention).
- No automatic rollback on `apply` failure — matches Slice 10's discipline of
  requiring human judgment on infrastructure failures rather than an
  automated guess.
- CD never touches runtime secrets (`hook-hmac-secret`, `graph-client-secret`,
  `password-payload-encryption-key`); those remain the Slice 10 Task 7 manual
  Key Vault injection process.

## 8. Azure Identity & RBAC

Created during implementation using the current session's authenticated
Azure identity (same pattern as Slice 10's "Deployment Identity Decision"),
with the outcome recorded in a decision document:

- One new Azure AD App Registration dedicated to CD (e.g.
  `sp-password-hook-cd-stg`), with exactly one Federated Credential whose
  subject is scoped to this repository's GitHub **Environment** `staging`
  (not "any branch" or "any PR"), to minimize what can assume this identity.
- Least-privilege RBAC, scoped to resource group / resource level, not
  subscription-wide:
  - `Contributor` on `rg-password-hook-stg-jpe-001`.
  - `AcrPush` on the existing ACR (`acrjpe001`).
  - `Storage Blob Data Contributor` on the tfstate storage account
    (`sttfstatephsjpe001`), matching the `use_azuread_auth = true` backend
    already documented in `deploy/terraform/README.md`.
  - A role assignment permission scoped to the resource group (e.g. `Role
    Based Access Control Administrator`, resource-group-scoped) so Terraform
    can create the runtime UAMI's role assignments (Service Bus, Key Vault,
    Redis, ACR pull, metrics publisher) as it already does today when applied
    manually.

## 9. Documentation

Update `deploy/terraform/README.md` (and/or root `README.md`) to describe,
for future developers and agents:

- The overall CD pipeline flow (§4–§7).
- What `TF_APPLY_MODE` is, what `plan` vs `apply` mean concretely, and the
  exact steps to flip from one to the other.
- Which Azure identity CD uses and its RBAC scope, so it's auditable without
  re-deriving it from the workflow YAML.
- That CD never touches runtime secrets, and where those are still injected
  manually.

## 10. Testing / Validation Strategy for This Plan

- `go test ./...`, `go vet ./...` (existing; unaffected by these changes,
  but still run to confirm no regression).
- Lint the new/changed workflow YAML (e.g. `actionlint`).
- `terraform -chdir=deploy/terraform fmt -check -recursive` and `validate`,
  including the new `staging.tfvars`.
- Open a real test PR to confirm all six CI jobs (`test`, `gosec`,
  `govulncheck`, `trivy-fs`, `gitleaks`, `terraform-check`) run in parallel,
  report correctly, and that SARIF results appear under the repository's
  Security tab.
- Land CD with `TF_APPLY_MODE=plan` first; a human reviews at least one clean
  `terraform plan` run against real staging before switching to `apply`
  (§7.4).

## 11. Completion Criteria

- Every PR against `main` runs `test`, `gosec`, `govulncheck`, `trivy-fs`,
  `gitleaks`, and `terraform-check`; `main` cannot be merged into without all
  six passing (branch protection enforces this).
- `gosec`/`trivy-fs`/`gitleaks` findings are visible as persistent GitHub
  Code Scanning alerts, not only transient CI log output.
- Merging to `main` builds and pushes a scanned image to the existing ACR and
  drives a real `terraform apply` against staging using OIDC federated
  identity (no stored Azure client secret), gated by the plan/apply safety
  valve having already been exercised once cleanly.
- The resulting Container App revision's health is verified via the Azure
  API before the CD job succeeds.
- `deploy/terraform/README.md` documents the CD pipeline and the
  `TF_APPLY_MODE` switch-over procedure clearly enough that a future
  developer or agent does not need to reverse-engineer it from workflow YAML.
- Production deployment, scheduled scans, ZAP DAST, and any new VNet-internal
  scanning infrastructure remain explicitly out of scope (§3).

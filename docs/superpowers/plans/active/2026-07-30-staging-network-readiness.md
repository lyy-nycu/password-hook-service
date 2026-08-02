# Staging Network Readiness and Controlled Apply Plan

> **Plan Status:** Active
>
> **Created:** 2026-07-30
>
> **Source of truth:** Start with
> `docs/handoffs/2026-07-28-staging-network-and-deployment.md`, then use
> `docs/superpowers/plans/active/2026-07-03-slice-10-infrastructure.md` and
> `docs/superpowers/plans/active/2026-07-03-slice-10-private-network-decisions.md`
> for the remaining Slice 10 gates.

**Goal:** Establish the approved on-premises route to the staging Application
Gateway private frontend, prove the real portal-to-application trust path with
a password-safe signed request, and only then run the already-built CD
pipeline's first controlled staging apply.

**Scope:** This is a focused operational continuation of Slice 10. It does not
redesign the application, Terraform modules, CI/CD, Application Gateway
objects, or production topology. Production rollout, Slice 12, new secrets,
and direct portal access to ACA are out of scope.

## Fixed Safety Boundaries

- Keep `TF_APPLY_MODE=plan` until Tasks 0-5 are complete.
- Make Azure network changes only through the confirmed state owner repository
  or pipeline. Do not create out-of-band peerings with ad hoc Azure CLI writes.
- Preserve the connected AGW-to-ACA peering and the existing password-hook
  private frontend/listener/rule/WAF/backend path.
- Never capture request bodies, raw passwords, HMAC values, signatures,
  nonces, VPN pre-shared keys, tokens, queue payloads, or full forwarded-header
  values.
- Stop if the live state differs from this plan, the owner is unknown, the
  rollback is unavailable, an unrelated AGW route regresses, or Terraform
  proposes an unreviewed core-resource delete/replacement.

## Read-Only Preflight Evidence (2026-07-30)

- Subscription `LGTW-PoC` is selected.
- VPN connection `s2s-az-juniper-jp-001` is `Connected`, uses policy-based
  traffic selectors, and has active ingress/egress byte counters.
- Hub VNet `vnet-hub-jp-001` and workload VNet `vnet-stg-jpe-001` have a
  connected gateway-transit/remote-gateway peering pair.
- AGW VNet `vnet-ag-stg-jpe-001` has only `agw-to-cae`; it has no direct hub
  peering. The routing gap therefore still exists.
- The existing `agw-to-cae`/`cae-to-agw` pair is connected and must remain
  unchanged.
- Application Gateway `agw-stg-jpe-001` is running with private frontend
  `10.0.8.62`, password-hook listener/WAF/rule priority `120`, and a `Healthy`
  password-hook ACA backend.
- The AGW subnet has no route table. Its NSG allows NYCU TCP 443 sources,
  including `140.113.0.0/16`; the listener-specific WAF remains the narrower
  `140.113.7.17/32` application boundary.
- The Local Network Gateway includes `140.113.7.0/24`.
- The deployed private-endpoint subnet is `10.0.4.96/27` and contains the Key
  Vault, Service Bus, and Managed Redis private endpoints.
- The `lyy-nycu/ldap-service` password-hook workflow manages only isolated
  Application Gateway objects, not VNet peerings. GitHub source search found
  no repository definition for either required peering. Azure activity logs
  show human peering writes but do not establish the long-term state owner.

### Ownership Decision (2026-07-30)

- The owner approved one dedicated shared-network IaC repository and state as
  the authoritative owner of both staging hub-to-AGW peering resources.
- **Created 2026-08-02:** the private repository
  [`lyy-nycu/azure-shared-network-infra`](https://github.com/lyy-nycu/azure-shared-network-infra)
  now exists with `main` as its default branch. It is the designated owner,
  but is not operational until its remote state, pipeline, protection, and
  approval gates are established.
- `lyy-nycu` is the initial change operator and has confirmed Azure network
  read/write access. Permission to write is execution authority, not a
  substitute for the reviewed repository, state, pipeline, and rollback path.
- `lyy-nycu/ldap-service` remains responsible for its existing isolated
  Application Gateway listener/WAF/backend workflow. It will not own the hub
  or AGW VNet peering state: network transport is shared infrastructure, not
  part of the LDAP query service's responsibility.
- `password-hook-service` will consume the resulting network path but will not
  import either shared VNet or peering into its application Terraform state.

### Task 0: Resolve the Network State Owner and Change Authority

**Files:**
- Update this plan with the confirmed owner, repository/pipeline, change
  window, and rollback authority.

- [x] Capture the read-only Azure and GitHub evidence above without making an
  Azure write.
- [x] Approve one dedicated shared-network IaC repository/state as the owner
  of both peering sides; exclude the application and LDAP query service states
  from that responsibility.
- [x] Record `lyy-nycu` as the initial change and rollback operator with
  confirmed Azure network read/write access.
- [x] Create and record the private shared-network repository:
  `lyy-nycu/azure-shared-network-infra`, with `main` as its default branch.
- [ ] Establish and record its remote state backend/key, protected default
  branch, plan/apply pipeline, and environment approval that will create and
  later remove both peering resources.
- [ ] Give the pipeline identity only the network permissions required at the
  two VNet scopes, and prove a plan can read both VNets before enabling apply.
- [ ] Record the named technical-team/Juniper owner for route,
  traffic-selector, firewall/no-SNAT, and split-horizon DNS changes.
- [ ] Record one approved staging change window and the operator who can
  execute rollback during that same window.
- [ ] Confirm that no competing Terraform/Bicep/pipeline state will reconcile
  either VNet after the change.

**Completion criterion:** The owner, write path, change window, and rollback
authority are explicit and reviewable. If any remain unknown, stop here; no
network write or `TF_APPLY_MODE` change is authorized.

### Task 1: Freeze the Baseline and Approve the Change Package

**Files:**
- Update this plan or the current handoff with a sanitized before-change
  summary. Do not commit raw command transcripts or sensitive traffic data.

- [ ] Re-run and record the names, state, and gateway-transit flags of all
  peerings on the hub, staging AGW, and staging workload VNets. Confirm the
  AGW VNet still has no remote-gateway peering.
- [ ] Record VPN provisioning/connection state and byte counters without
  recording the VPN pre-shared key or device configuration.
- [ ] Record the AGW provisioning state, private frontend `10.0.8.62`,
  password-hook listener/WAF/rule priority `120`, backend pool/settings, and
  `Healthy` backend result.
- [ ] Run the existing `lyy-nycu/ldap-service` public LDAP synthetic check and
  record only the HTTP status/result needed for before/after comparison.
- [ ] Confirm the AGW subnet NSG still permits TCP 443 from the required NYCU
  source range, has no route table, and has no unexpected delegation.
- [ ] Review the owner-managed change and verify its desired state is exactly:
  - hub to staging AGW: virtual-network access, forwarded traffic, and gateway
    transit enabled; remote-gateway use disabled;
  - staging AGW to hub: virtual-network access, forwarded traffic, and remote
    gateway enabled; gateway transit disabled;
  - existing AGW-to-ACA and ACA-to-AGW peerings unchanged.
- [ ] Review rollback in the same owner pipeline: remove only the two new
  peerings, AGW-to-hub first and hub-to-AGW second, then prove the original
  peering inventory, VPN connection, AGW backend health, and public LDAP route
  are restored.
- [ ] Obtain explicit go/no-go approval for the reviewed change package and
  change window.

**Completion criterion:** The pre-change snapshot, exact desired state,
owner-managed implementation, regression checks, and two-sided rollback are
reviewed before the first network write.

### Task 2: Add the Two Owner-Managed Hub-to-AGW Peerings

**Files:**
- Change only the confirmed network owner repository/pipeline.
- Record the reviewed PR/run identifiers in this plan or the current handoff.

- [ ] Immediately before execution, confirm Task 1's baseline still matches
  live Azure state. Stop on any drift.
- [ ] Through the approved owner pipeline, create the hub-to-staging-AGW
  peering with virtual-network access, forwarded traffic, and gateway transit
  enabled.
- [ ] Through the same reviewed change package, create the staging-AGW-to-hub
  peering with virtual-network access, forwarded traffic, and remote-gateway
  use enabled.
- [ ] Wait for both new peerings to report `Connected`; do not treat a
  submitted or provisioned write as completion.
- [ ] Confirm `s2s-az-juniper-jp-001` remains `Connected` and continues to
  transfer traffic.
- [ ] Confirm `agw-to-cae`, `cae-to-agw`, `stg-jpe-to-hub`, and
  `hub-to-stg-jpe` retain their original flags and `Connected` state.
- [ ] Confirm `agw-stg-jpe-001` remains running, its password-hook backend
  remains `Healthy`, and the unrelated public LDAP synthetic check has not
  regressed.
- [ ] If either side fails or a regression appears, run the reviewed rollback
  during the same window and attach only sanitized results.

**Completion criterion:** The two new peerings are `Connected`, the VPN and
existing peerings are unchanged, and both password-hook backend health and the
unrelated public route pass their regression checks.

### Task 3: Complete the Juniper Route, No-SNAT, and Split-Horizon DNS Change

**Files:**
- Change only the technical-team-owned Juniper, firewall, and DNS systems.
- Record the approved change ticket/window and sanitized outcome in the
  current handoff.

- [ ] Give the network team the Azure destination `10.0.8.0/24`, private
  frontend `10.0.8.62`, TCP port `443`, hostname
  `api.test.nycu.edu.tw`, and expected portal source `140.113.7.17`.
- [ ] Have the network team update the policy-based VPN selector/route so the
  portal host can reach `10.0.8.0/24` over the existing tunnel. Do not change
  the established VPN gateway or create a second tunnel.
- [ ] Confirm the portal request is not source-NATed. The Application Gateway
  WAF must observe `140.113.7.17`; any other source will be rejected.
- [ ] Limit the on-premises firewall change to the required portal source and
  `10.0.8.62:443`. Do not permit portal access to ACA `10.0.4.55`, the ACA
  VNet, private endpoints, or other AGW addresses.
- [ ] Add the on-premises split-horizon DNS answer
  `api.test.nycu.edu.tw -> 10.0.8.62`. Do not change public authoritative DNS
  or the existing public LDAP/ACME path.
- [ ] Verify the portal resolver returns only the private frontend while an
  external/public resolver continues to return the existing public answer.
- [ ] Confirm the technical team can roll back the selector/route/firewall and
  private DNS record within the approved window.

**Completion criterion:** The approved portal host has a private DNS answer
and a narrowly scoped, no-SNAT TCP 443 route to the AGW private frontend, with
no direct ACA path and no public DNS regression.

### Task 4: Prove DNS, Transport, TLS, WAF Source, and Trust-Path Behavior

**Files:**
- Append only sanitized results to the current handoff and the Slice 10
  private-network decision.

- [ ] From the real staging portal host, resolve
  `api.test.nycu.edu.tw` and confirm the answer is `10.0.8.62`.
- [ ] From that host, prove TCP 443 reachability and a successful TLS
  handshake using `api.test.nycu.edu.tw` as SNI; verify the presented chain
  against the portal host's trust store.
- [ ] Call `GET /healthz` through the private hostname and record only the
  HTTP status; require `200`.
- [ ] In sanitized AGW/WAF evidence, confirm the observed source is exactly
  `140.113.7.17`, the password-hook listener-specific WAF policy handled the
  request, and managed inspection was not bypassed.
- [ ] Confirm the portal host cannot connect directly to ACA `10.0.4.55`, the
  ACA backend FQDN as a bypass path, or the private-endpoint subnet.
- [ ] Correlate the WAF result and application response using a trace/request
  identifier that contains no request content or authentication material.
- [ ] Use the accepted request behavior to prove the configured trust path:
  the request reached the application through the AGW boundary and resolved
  to the allowed portal source. Do not record the full `X-Forwarded-For`
  value.
- [ ] If the platform/application evidence cannot establish that the immediate
  peer is within `10.0.8.0/26` and the resolved client is the portal source,
  stop and create a separate security-reviewed observability change. Do not
  widen `TRUSTED_PROXY_CIDRS` or trust an unvalidated forwarded value.

**Completion criterion:** The real portal host reaches only the private AGW
listener with valid TLS, WAF observes the approved no-SNATed source, `/healthz`
returns `200`, direct ACA access is unavailable, and the trusted-proxy
behavior is evidenced without logging sensitive headers.

### Task 5: Run One Approved Password-Sync End-to-End Test

**Files:**
- Record only redacted status, resource, trace, and metric evidence in the
  current handoff.

- [ ] Before generating a request, approve a dedicated staging test identity,
  its expected Graph create-or-patch outcome, owner, lifetime, and cleanup or
  password-rotation procedure. Do not use an arbitrary numeric `cn`: an
  accepted request can create or update a real Entra member account.
- [ ] Confirm the test identity is isolated from real student/employee access
  and that the selected `eventType` produces the intended queue behavior.
- [ ] Provide the test password and HMAC material only through approved secret
  mechanisms on the portal host. Do not place either value in a command
  argument, shell history, plan, ticket, log, or captured output.
- [ ] From the approved portal host, send exactly one signed request through
  `https://api.test.nycu.edu.tw/api/v1/hook/password` and record only the
  response status and safe correlation identifier. Require `202 Accepted`.
- [ ] Verify, without reading or exporting the queue body, that one eligible
  message was accepted and consumed before its five-minute TTL.
- [ ] Verify the worker's expected Graph outcome and the corresponding
  `synced` or explicitly understood failure transition. `202` alone is not a
  successful migration result.
- [ ] Confirm WAF, application, worker, Service Bus, safe-DLQ, and telemetry
  evidence contains no plaintext password, request body, HMAC value,
  signature, nonce, token, ciphertext fields, or raw queue payload.
- [ ] Execute the approved test-account cleanup or password-rotation step and
  record only its completion status.

**Completion criterion:** One approved staging identity completes the intended
hook, queue, worker, Graph, and sync-status path through the private listener,
with password-safe evidence and an executed account cleanup/rotation.

### Task 6: Run the First Controlled CD Staging Apply

**Files:**
- Use the existing `.github/workflows/cd.yml` and
  `deploy/terraform/environments/staging.tfvars`.
- Record the reviewed run and sanitized outcome in the current handoff.

- [ ] Define and review an executable rollback for the previous known-good
  Container App image and every non-image plan change. If the current CD
  workflow cannot select the previous image or otherwise execute that
  rollback, stop and implement a separate reviewed rollback capability before
  enabling apply.
- [ ] Confirm Tasks 0-5 are complete, no network change is still propagating,
  no CD run is active/queued, and `TF_APPLY_MODE` is `plan`.
- [ ] Dispatch a fresh plan from the exact current `main` commit and review it
  against the accepted first-apply baseline: `1 add / 3 change / 1 destroy`
  for the image, provider-normalized Container App/Service Bus values, and
  Monitoring Metrics Publisher role replacement.
- [ ] Stop if the plan deletes/replaces a core application, network, Key
  Vault, Service Bus namespace, Managed Redis, identity, private endpoint, or
  operator-access resource, or if any change is not explicitly understood.
- [ ] Capture the current Container App revision/image, Terraform state
  serial, runtime identity/RBAC, AGW backend health, VPN state, and portal
  `/healthz` result needed for rollback comparison. Record no secret values.
- [ ] Set `TF_APPLY_MODE=apply` only for the approved maintenance run and
  immediately dispatch the reviewed `main` commit.
- [ ] Monitor build, Trivy image scan, OIDC login, ACR push, Terraform
  init/plan/apply, and Container App revision health. Do not treat a successful
  image push or Terraform plan as a successful apply.
- [ ] Regardless of success or failure, immediately reset
  `TF_APPLY_MODE=plan` and verify the repository variable before any other
  push or dispatch.
- [ ] On success, confirm the active revision is `Provisioned`/`Running`, the
  AGW backend remains `Healthy`, the VPN remains `Connected`, and the real
  portal host still receives `200` from `/healthz`.
- [ ] Verify ACR pull, Key Vault private secret access, Service Bus
  send/receive and scaler authentication, Managed Redis Entra/TLS, private DNS
  resolution, and restoration of the Monitoring Metrics Publisher assignment
  without a public-network fallback.
- [ ] If apply or health verification fails, execute the reviewed rollback
  within the same window, keep `TF_APPLY_MODE=plan`, and record the sanitized
  failure/rollback result before retrying.

**Completion criterion:** The exact reviewed plan is applied once, the safety
variable is restored to `plan`, the revision and all ingress/PaaS paths are
healthy, and rollback remains proven and available.

### Task 7: Reconcile Evidence, Close Slice 10, and Prepare the Next Handoff

**Files:**
- Update `docs/handoffs/2026-07-28-staging-network-and-deployment.md`.
- Update `docs/superpowers/plans/active/2026-07-03-slice-10-infrastructure.md`.
- Update
  `docs/superpowers/plans/active/2026-07-03-slice-10-private-network-decisions.md`.
- Update `docs/superpowers/plans/README.md` and
  `docs/superpowers/plans/roadmap.md`.

- [ ] Record the authoritative network state owner, owner repository/pipeline,
  reviewed PR/run or change-ticket identifiers, operators, change window, and
  sanitized before/after results in the current handoff.
- [ ] Reconcile the original Slice 10 checklists against completed Tasks 0-6.
  Check an item only when its own evidence exists; retain partial-verification
  notes where one portal host or one test cannot satisfy a broader statement.
- [ ] Record the actual DNS answer, TLS result, WAF-observed source, immediate
  application peer/trusted-proxy result, direct-ACA denial, hook status,
  queue/worker/Graph outcome, and cleanup status without request content or
  authentication material.
- [ ] Record the controlled CD run, applied Terraform summary, active
  revision/image identifier, PaaS-path results, rollback disposition, and
  final `TF_APPLY_MODE=plan` verification.
- [ ] Re-run a state/config/documentation scan for passwords, HMAC material,
  signatures, nonces, VPN pre-shared keys, tokens, raw queue payloads,
  ciphertext fields, request bodies, and full forwarded-header values. Remove
  any unsafe evidence before commit or upload.
- [ ] Confirm production remains unchanged and blocked until every staging
  completion criterion below passes.
- [ ] If every Slice 10 criterion is evidenced, move this focused plan and the
  Slice 10 infrastructure/decision documents to `completed/`, update all
  inbound links, and mark Slice 10 done in the roadmap. Otherwise leave them
  active and list each unresolved gate explicitly.
- [ ] Create or refresh a separate Slice 12 plan only after Slice 10 is
  complete. Do not begin Slice 12 or production rollout as part of this plan.
- [ ] Run `git diff --check`, validate all relative Markdown links, review the
  final changed-file list, and confirm no application, Terraform, workflow, or
  Azure resource was changed by the documentation closeout itself.

**Completion criterion:** The repository points to one evidence-backed
current status, Slice 10 is marked complete only if every live gate passed,
production remains protected, and the next slice has a clean handoff without
stale or contradictory instructions.

## Overall Completion Criteria

- The authoritative owner and reviewed write path for both hub-to-AGW peering
  resources are recorded, with no competing state owner.
- Both new staging peerings are connected with the approved gateway-transit
  flags; the VPN, existing peerings, Application Gateway backend, and
  unrelated public LDAP route remain healthy.
- The real staging portal host resolves `api.test.nycu.edu.tw` to `10.0.8.62`
  and reaches only that private listener over no-SNAT TCP 443 with valid TLS.
- WAF observes `140.113.7.17`, managed inspection remains active, the
  application trusts only the measured AGW subnet peer, and direct ACA/private
  endpoint access is unavailable from the portal host.
- One approved staging identity completes the hook-to-Graph path with
  `202 Accepted`, expected queue/worker/sync-status behavior, password-safe
  evidence, and completed cleanup or password rotation.
- The exact reviewed first-apply Terraform plan runs once through CD, all
  runtime and private PaaS paths remain healthy, rollback is executable, and
  `TF_APPLY_MODE` is verified as `plan` afterward.
- Evidence contains no secrets, credentials, request bodies, raw queue
  payloads, ciphertext fields, or sensitive forwarded-header content.
- Production and Slice 12 remain blocked until all criteria above are
  evidenced and Slice 10 documentation is reconciled.

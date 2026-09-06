# E2E Rerun (Autopilot) — Handoff and Resolution

**Date:** 2026-09-06 (interrupted mid-execution, then resumed and completed
same day)

**Status:** **Resolved.** The objective completed successfully and all
temporary staging changes were reverted. See *Resolution* below for the
final outcome; the rest of this document is preserved as the historical
record of the interrupted state and required cleanup steps that were then
carried out exactly as written.

## Resolution (final outcome)

The objective was resumed and completed successfully:

- The first signed request (using the PR #30 widening, `10.0.8.4/32`) was
  rejected with `401 source_ip_not_allowed`. Container-log diagnostics
  (`sourceResolution=trusted_forwarded peerIp=100.100.0.81
  resolvedClientIp=10.0.8.6`) showed the AGW private-frontend instance
  resolved a **different** address this time than the 2026-09-01/02 and
  first-attempt sessions, even with `sku.capacity=1` and no autoscale
  configured — AGW instances get a dynamically allocated address from their
  own subnet across restarts/redeploys, so a single `/32` is not a reliable
  long-term target. Fixed with
  [PR #31](https://github.com/lyy-nycu/password-hook-service/pull/31),
  widening to the full `agw-subnet` CIDR (`10.0.8.0/26`, confirmed via `az
  network vnet subnet show`) for the remainder of the test window.
- The retried signed `password_change` request returned `202`. Container
  logs confirmed the full pipeline: `hook_password_sync_accepted
  outcome=enqueued` → `graph_password_upsert outcome=success durationMs=650`
  → `worker_password_sync_completed outcome=synced attempts=1`
  (`traceId=cd51ed25-b7c6-4413-af35-7a43e4d75729`).
- Confirmed via a live Graph API call (not just the container logs): the
  account exists, `accountEnabled: true`,
  `createdDateTime: 2026-09-06T13:21:23Z` (Graph object id
  `77f00714-8c06-4323-a622-5ccf20d46ea4`) — matching the log timestamp
  exactly. `e2e-test-credentials.txt` is now confirmed login-ready and its
  header comment was updated accordingly.
- `password-sync-safe-dlq` was rechecked post-test: still exactly 2 messages
  (the pre-existing 2026-09-02 leftovers) — this test's single attempt
  succeeded cleanly and added no new DLQ messages.
- All temporary changes were then reverted and verified:
  - WAF `BlockNonPortalSources` back to `matchValues: ["140.113.7.17/32"]`
    only (verified via `custom-rule list`).
  - `portal_allowed_cidrs` reverted to `["140.113.7.17/32"]` only via
    [PR #32](https://github.com/lyy-nycu/password-hook-service/pull/32),
    confirmed live on the deployed Container App (checked the
    `PORTAL_ALLOWED_CIDRS` env var directly, not just git).
  - `TF_APPLY_MODE` repo variable set back to `plan`.
  - The `Key Vault Secrets User` role assignment for the VM identity on
    `hook-hmac-secret` removed (verified the scope's role-assignment list is
    now empty).
  - `vm-s2stest-jp-001` deallocated (verified `PowerState: deallocated`).
- **Kept, intentionally, per the objective:** the new Entra account
  (`e2e-20260906-065346@nycumis.onmicrosoft.com`) and
  `e2e-test-credentials.txt`. Unlike the 2026-09-01/02 session, this account
  was **not** deleted/purged — it is meant for the user's manual Entra ID
  login testing. Whoever finishes using it should delete/purge the account
  and delete the credentials file (instructions are in the file's own
  header).

### PRs opened this session

| PR | Purpose | Outcome |
|---|---|---|
| #30 | Widen `portal_allowed_cidrs` +`10.0.8.4/32` | Merged, later found insufficient |
| #31 | Widen `portal_allowed_cidrs` to full `10.0.8.0/26` | Merged, fixed the flaky single-IP issue |
| #32 | Revert `portal_allowed_cidrs` to baseline | Merged, final cleanup |

---

## Original interrupted-state record (historical, kept for reference)

**Original status when this document was first written:** Interrupted by
the user partway through execution. Several temporary, security-relevant
staging changes were live/open and not yet reverted. The section below
describes that in-flight state and the plan that was then followed to
resolve it (see *Resolution* above for what actually happened).

## Objective that was in progress

Autopilot objective (verbatim intent): rerun the staging password-hook-service
E2E test (same class of validation as
[2026-09-01/02](./2026-08-06-staging-shared-network-remote-plan.md#staging-api-e2e-validation-and-graph-upsert-creates-user-finding-2026-09-0102))
and save a synthetic username/password to a workspace-root file, so the user
can manually log into Entra ID with that identity for manual testing. Unlike
the 2026-09-01/02 session, the created Entra account was meant to be **kept**
(not deleted/purged) this time.

## What actually happened before the interrupt

1. Reconnaissance confirmed the 2026-09-02 end state was intact: hub test VM
   deallocated, `TF_APPLY_MODE=plan`, `portal_allowed_cidrs` only
   `140.113.7.17/32`, WAF custom rule only `140.113.7.17/32`,
   `password-sync-safe-dlq` still holding the 2 pre-existing leftover messages
   from the prior session (unrelated to this one, still unaddressed).
2. Granted the hub VM's managed identity (`principalId
   ed4b497f-d2d1-482b-942d-2159927300eb`) the **Key Vault Secrets User** role,
   scoped to exactly the `hook-hmac-secret` secret in `kvpwdhookstgmvxfna`.
3. Started the hub test VM (`vm-s2stest-jp-001`, `RG-VPNGW-JP-001`). Confirmed
   running, private IP `192.168.10.4` (unchanged from prior session).
4. Widened the WAF custom rule `BlockNonPortalSources` on
   `waf-policy-password-hook-stg` (`rg-spoke-paas`) directly via
   `az network application-gateway waf-policy custom-rule update` (this
   policy is **not** Terraform-managed by this repo — it lives in the
   network team's shared infra and was edited directly via CLI, matching how
   the 2026-09-01/02 session must have done it, since PRs #26-28 only ever
   touched this repo's `portal_allowed_cidrs`, never the WAF policy).
   `matchValues` is now `["140.113.7.17/32", "192.168.10.4/32"]`,
   `negationConditon: true` (i.e. it still blocks everyone *not* in this
   list — just widened by one entry).
5. Branched `test/temp-portal-allowlist-e2e-20260906` off `main`, changed
   `deploy/terraform/environments/staging.tfvars` `portal_allowed_cidrs` to
   `["140.113.7.17/32", "10.0.8.4/32"]`, opened
   [PR #30](https://github.com/lyy-nycu/password-hook-service/pull/30), set
   the `TF_APPLY_MODE` repo variable to `apply`, waited for CI, merged
   (squash) as commit `9e7c939`.
6. CD run
   [`34017653520`](https://github.com/lyy-nycu/password-hook-service/actions/runs/34017653520)
   was triggered by that merge. **Update: confirmed successful after this
   handoff was first written** — `Terraform apply` and `Verify Container App
   revision health` both passed (6m21s total). `portal_allowed_cidrs =
   ["140.113.7.17/32", "10.0.8.4/32"]` is therefore **fully live**: applied to
   Terraform state and deployed/healthy on the running Container App
   revision, not merely merged in git.
7. Generated a fresh synthetic identity (CN `e2e-20260906-065346`, UPN
   `e2e-20260906-065346@nycumis.onmicrosoft.com`) and a strong random
   password, and wrote them to `e2e-test-credentials.txt` in the workspace
   root (already covered by a new `.gitignore` entry, file permissions
   `600`).
8. Designed (but **never executed**) the in-VM script to fetch
   `hook-hmac-secret` via the VM's managed identity + IMDS, compute the
   `X-Hook-*` HMAC signature, and POST one `password_change` request to
   `https://api.test.nycu.edu.tw/api/v1/hook/password` through the real AGW
   private frontend. The interrupt happened while watching the CD run, before
   this script was ever sent via `az vm run-command invoke`.

## Critical: no signed request was ever sent

**The Entra account described by `e2e-test-credentials.txt` does not exist
yet.** The file contains a pre-generated username and password that were
prepared *in advance* of sending the signed request, but the request itself
was never sent — step 8 above never ran. Do not treat that file as
login-ready. If someone tries those credentials against Entra ID before the
E2E request is actually sent and confirmed successful, the login will fail
because the account was never created.

## Exact live/open state as of interrupt (must be reverted)

All of the following are currently **live** deviations from the normal safe
baseline and should be reverted regardless of whether the objective is
resumed:

| Item | Normal baseline | Current (live) state |
|---|---|---|
| WAF `BlockNonPortalSources` matchValues | `["140.113.7.17/32"]` | `["140.113.7.17/32", "192.168.10.4/32"]` |
| `portal_allowed_cidrs` (staging.tfvars, merged to main) | `["140.113.7.17/32"]` | `["140.113.7.17/32", "10.0.8.4/32"]` (commit `9e7c939`, PR #30) |
| Repo variable `TF_APPLY_MODE` | `plan` | `apply` |
| Key Vault role on `hook-hmac-secret` | none for the VM identity | `Key Vault Secrets User` granted to `ed4b497f-d2d1-482b-942d-2159927300eb` |
| `vm-s2stest-jp-001` power state | deallocated | **running** |
| CD run `34017653520` | — | **confirmed successful** (Terraform apply + health check both passed) — widening is fully deployed, no need to re-check |

Local git working tree: on branch `test/temp-portal-allowlist-e2e-20260906`
(already merged to `main` via squash — safe to switch back to `main` and
pull). One uncommitted, harmless change: `.gitignore` gained an entry for
`e2e-test-credentials.txt` (no secrets in that diff; safe to commit
separately whenever convenient).

Pre-existing, unrelated leftover (documented on 2026-09-02, still not
cleaned, not made worse by this session): `password-sync-safe-dlq` holds 2
old messages from the prior E2E session (contain no password material,
ciphertext or plaintext — see struct-level analysis in that day's chat
history; not repeated here per the no-payload-content rule below).

## Required next actions, in order

1. ~~Check CD run `34017653520`'s final outcome.~~ **Done — confirmed
   successful.** Terraform state and the deployed Container App both already
   reflect the widened `portal_allowed_cidrs`; no re-plan needed.
2. Decide whether to resume the objective (send the one signed request, then
   keep the resulting account) or abandon it. Either way, proceed to cleanup:
3. Revert the WAF custom rule to `matchValues: ["140.113.7.17/32"]` only
   (same `az network application-gateway waf-policy custom-rule update`
   pattern used to widen it).
4. Open and merge a revert PR for `portal_allowed_cidrs` back to
   `["140.113.7.17/32"]` only (mirror PR #28's pattern), with
   `TF_APPLY_MODE=apply` for that one CD run.
5. Set the `TF_APPLY_MODE` repo variable back to `plan`.
6. Remove the `Key Vault Secrets User` role assignment for
   `ed4b497f-d2d1-482b-942d-2159927300eb` on the `hook-hmac-secret` secret.
7. Deallocate `vm-s2stest-jp-001`.
8. If resuming: send the one signed `password_change` request from the VM,
   confirm `202` + container logs show `graph_password_upsert
   outcome=success` + `worker_password_sync_completed outcome=synced` for
   UPN `e2e-20260906-065346@nycumis.onmicrosoft.com`, confirm via Graph the
   account now exists, *then* re-verify `e2e-test-credentials.txt` matches
   what was actually sent (CN/UPN do; the password in the file was generated
   locally and used verbatim in the request payload, so it should already be
   correct — but re-verify before handing it to anyone).
9. If abandoning: delete `e2e-test-credentials.txt` (it describes a
   never-created account and has no further purpose).

## Security note (per this repo's handoff convention)

No secrets, credentials, raw password values, request bodies, queue
payloads, signatures, or tokens are recorded in this file, consistent with
`docs/handoffs/README.md`. The synthetic password is only ever in
`e2e-test-credentials.txt` (workspace-root, `chmod 600`, git-ignored, never
committed).

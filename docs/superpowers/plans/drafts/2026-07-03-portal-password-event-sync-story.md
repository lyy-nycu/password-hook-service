# Portal Password Event Sync Story Draft Implementation Plan

> **Status:** Draft. This plan is a future-slice planning artifact only. Do not execute it until Slice 7 is completed or explicitly paused, this draft is refreshed against `main`, the source design is updated, and the plan is promoted to `docs/superpowers/plans/active/`.
>
> **For agentic workers:** REQUIRED SUB-SKILL WHEN PROMOTED: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct the portal integration story so the password hook receives password-bearing events for initial active-user bootstrap, password recovery, and password change, while the hook service state prevents ordinary logins from repeatedly syncing already-confirmed accounts to Entra ID.

**Architecture:** Split the current single "successful login sends password" story into explicit password event types and a durable sync-status model. The portal remains responsible for sending password events when it has a valid cleartext password, while the hook service and worker own the authoritative synced state because only the worker can know whether Microsoft Graph create/patch actually succeeded.

**Tech Stack:** Go HTTP handler and migration service, existing HMAC middleware, encrypted Service Bus messages, worker/Graph processor success path, a future durable sync-status store, PHP portal integration docs, unit and integration tests.

---

## Draft Constraints

- Do not execute this draft while Slice 7 Password Data Protection is active unless the owner explicitly promotes this as a replacement or parallel active slice.
- Do not update `docs/superpowers/plans/README.md`, `docs/superpowers/plans/roadmap.md`, or any active plan pointer while this remains a draft.
- Refresh this draft against the final Slice 7 implementation before promotion because Slice 7 may change password lifecycle, zeroing, leak tests, or worker plaintext handling.
- Keep ordinary portal login out of the steady-state Entra write path. In Phase 1 the portal may still call the hook on login, but `login_bootstrap` must become a hook-side no-op once an internal account is confirmed synced.
- Keep `202 Accepted` semantics unchanged: it means the hook accepted the event or skipped it, not that Entra ID has been updated.
- Do not let the portal mark an account as synced merely because the hook returned `202`. The authoritative synced transition happens only after the worker receives a successful Microsoft Graph create/patch result.
- Do not store plaintext passwords in the sync-status store, logs, safe DLQ, portal flags, or docs examples.

## Corrected Story

The password hook service is not a "sync on every login" service. It is a password-event ingestion service for moments when the portal legitimately has a fresh cleartext password after successful LDAP validation or update.

Supported portal event types:

| Event | Portal trigger | Hook behavior |
|---|---|---|
| `login_bootstrap` | A user successfully logs in and the portal has a valid LDAP password. | Enqueue only if hook-owned state says the account is still unsynced, failed, or stale-pending. If already synced or actively pending, return `202` without enqueueing. |
| `password_change` | The user successfully changes the LDAP password in the portal. | Always enqueue for eligible internal identities because this is a new password value. |
| `password_recovery` | The user completes recovery and successfully sets a new LDAP password. | Always enqueue for eligible internal identities because this is a new password value. |

Phase 1 keeps the portal simple: login, password change, and password recovery all call the hook, and the hook service decides whether the event should enqueue work. Once an account is confirmed `synced`, ordinary `login_bootstrap` calls must not enqueue or patch Graph again. A later optimization can let the portal suppress already-synced login hook calls by reading a status API, local cache, callback-updated flag, or polling feed.

External email identities remain out of Phase 1 migration. The hook returns `202 Accepted` for external email identities and skips enqueue, preserving the portal login and account-recovery user experience.

## Sync Status Model

The sync-status source of truth belongs to the hook service and worker, not the portal.

| Status | Meaning | Phase 1 login behavior |
|---|---|---|
| `unsynced` | No worker-confirmed successful Graph create/patch exists for this internal `cn` or `upn`. | `login_bootstrap` enqueues. |
| `sync_pending` | A password event has been accepted/enqueued, but Graph success has not been confirmed. | Active pending bootstrap returns `202` without duplicate enqueue; stale pending may enqueue according to the retry policy. |
| `synced` | The worker confirmed Microsoft Graph create/patch succeeded for the latest accepted password event. | `login_bootstrap` returns `202` without enqueueing. |
| `sync_failed` | The worker exhausted retry or wrote a password-safe DLQ record. | A later eligible `login_bootstrap` may enqueue again, or operations can trigger manual remediation. |

The worker transitions an account to `synced` only after the Graph client successfully creates a missing Entra user or patches an existing user's password. Transient errors, permanent Graph failures, safe DLQ writes, and message enqueue acceptance do not create a synced state.

## Recommended Roadmap Placement

Recommended placement: insert a new slice after Slice 7 and before Slice 8, tentatively named **Slice 7A Portal Password Event Semantics and Sync Status**.

Rationale:

- This changes the core product contract between portal, hook API, queue, worker, and Graph. It is broader than documentation but should land before observability, API protection, infrastructure, and production readiness bake in the old "every successful login" story.
- It depends on Slices 2, 4, and 6 because enqueue, worker processing, and Graph success classification already exist.
- It should follow Slice 7 because the new event model still carries cleartext passwords through the hook boundary and must preserve the finalized password zeroing and leak-prevention behavior.
- It should precede Slice 8 because metrics and structured logs need event labels such as `login_bootstrap`, `password_change`, and `password_recovery`, plus sync-status outcomes.
- It should precede Slice 9 because API protection and portal behavior docs should describe the correct production traffic pattern: bootstrap logins only until synced, plus password update events.
- It should precede Slice 10 because infrastructure may need durable storage for sync status and possibly a status lookup or callback path for the portal.

Do not fold this into Slice 12. Waiting until production readiness would leave earlier observability, API protection, infrastructure, and CI/CD plans aligned to an incorrect portal contract.

## Downstream Draft Tracking

Do not refresh Slice 8, Slice 9, Slice 10, Slice 11, or Slice 12 against this story until the owner confirms the Slice 7A event/status semantics. Once confirmed, update downstream drafts before promotion:

| Draft | Required refresh after Slice 7A confirmation |
|---|---|
| Slice 8 Observability | Add metrics and logs for `event_type`, hook `decision`, sync-status transitions, already-synced bootstrap no-ops, password-change/password-recovery resyncs, pending/stale-pending outcomes, and failure-to-sync reasons. |
| Slice 9 API Protection | Replace the stale "each hook request represents one successful-login password event" story with the corrected event model: login bootstrap, password change, and password recovery. Recheck rate-limit expectations because Phase 1 may still call hook on ordinary login but no-op after synced. |
| Slice 10 Infrastructure | Account for any durable sync-status store, retention policy, backups, secret/access model, and possible future portal status lookup/callback path. |
| Slice 11 CI/CD and Security Gates | Add tests/scans that cover event-type validation, sync-status behavior, no plaintext status persistence, and observability labels without secret material. |
| Slice 12 Integration and Production Readiness | Update portal integration guide, staging smoke tests, operations runbooks, dashboards, alerts, and DLQ review procedures around event-aware sync status. |

Minimum Slice 8 metric requirements after refresh:

- `password_events_total{event_type,decision}` where `decision` includes `enqueued`, `skipped_already_synced`, `skipped_pending`, `skipped_external_identity`, and `rejected`.
- `password_sync_status_transitions_total{from,to,event_type,reason}` for worker-confirmed success, enqueue-to-pending, retry exhaustion, permanent failure, and recovery/change re-sync.
- `password_sync_current{status}` or an equivalent gauge if the chosen sync-status store can expose aggregate state cheaply.
- Existing worker and Graph metrics must include safe non-secret labels that let operators distinguish bootstrap sync from password update sync.

Metric labels must never include plaintext passwords, password ciphertext, password nonce, password key IDs, request bodies, Graph request bodies, Graph tokens, or high-cardinality opaque secrets.

## Future Implementation Scope

Likely files and responsibilities when promoted:

- Modify `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`: replace the every-login migration story with event-aware bootstrap/update semantics.
- Modify `README.md`: document portal event types, `202` semantics, hook-side bootstrap no-op behavior after confirmed sync, and the later optional portal-side suppression path.
- Modify `docs/examples/sign-hook-request.php`: include an example event type field.
- Modify `internal/handler/hook.go`: accept and validate a non-secret event type field.
- Modify `internal/migration/message.go`: carry the event type through encrypted queue metadata.
- Modify `internal/migration/service.go`: apply bootstrap dedupe or status checks before enqueueing `login_bootstrap`.
- Create or modify a sync-status package: record `unsynced`, `sync_pending`, `synced`, and `sync_failed` transitions without storing password material.
- Modify `internal/worker/worker.go` or the Graph processor boundary: mark `synced` only after successful Microsoft Graph create/patch.
- Modify tests across handler, migration, worker, and app wiring to prove that `password_change` and `password_recovery` enqueue even after prior sync, while `login_bootstrap` does not enqueue once synced.
- Modify portal integration documentation: require Phase 1 portal code to send login bootstrap, password change, and password recovery events; document that hook-owned state suppresses duplicate Graph work for already-synced bootstrap logins; document optional later portal-side suppression after a status propagation mechanism exists.

## Acceptance Criteria For Promotion

- The source design no longer says every successful portal login should call the hook.
- The API contract includes an event type or equivalent field that distinguishes `login_bootstrap`, `password_change`, and `password_recovery`.
- `login_bootstrap` is deduped by hook-owned state after worker-confirmed sync.
- `password_change` and `password_recovery` always enqueue eligible internal identities because they represent a new password value.
- The synced state is created only from worker-confirmed Graph success, never from hook `202 Accepted`.
- Tests cover accepted bootstrap, skipped already-synced bootstrap, password-change resync, password-recovery resync, external email skip, and failure-to-sync status behavior.
- Leak-focused tests or scans still prove plaintext passwords are not logged, persisted outside encrypted queue payloads, or written to safe DLQ.

## Draft Self-Review

- Spec coverage: captures initial Entra bootstrap, hook-side steady-state Graph suppression, password recovery/change behavior, worker-owned success state, optional later portal-side call suppression, and `202 Accepted` semantics.
- Placeholder scan: no deferred placeholder text is used.
- Scope check: this is large enough for its own slice because it spans API contract, portal integration docs, sync state, migration decisions, and worker success handling.
- Ambiguity check: synced means worker-confirmed Microsoft Graph success, not hook acceptance or enqueue success.

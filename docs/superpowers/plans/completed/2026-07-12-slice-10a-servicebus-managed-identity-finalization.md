# Slice 10A Service Bus Managed Identity Finalization Plan

> **Plan Status:** Completed
>
> **Scope:** Complete the documentation and verification work identified after the Slice 10A implementation review. This plan does not add Azure infrastructure, RBAC assignments, or live Azure validation.
>
> **Completion Note:** Tasks 1-3 passed in the final worktree review on 2026-07-12. Commit the working-tree changes and rerun `git diff --check main...HEAD` before creating a pull request, because that comparison only evaluates committed history.

**Goal:** Make the documented managed-identity deployment path match the completed application behavior, correct the handover record, and leave Slice 10A ready for final review.

**Completion criteria:** The README distinguishes local connection-string fallback from production managed identity and RBAC, whitespace checks pass, the handover reports actual verification results, focused/full Go checks and the leak scan pass, and the original Slice 10A plan records the finalization outcome.

### Task 1: Correct Managed Identity Runtime Documentation [completed]

**Files:**
- Modify: `README.md`

**Source verified:** `README.md` currently documents `SERVICEBUS_AUTH_MODE=managed_identity` and the namespace FQDN, but its following paragraph still recommends a Shared Access Policy. Its generic `docker run` command forwards `SERVICEBUS_CONNECTION_STRING` but not the managed-identity mode or namespace FQDN.

1. Replace the Shared Access Policy instruction with a `Service Bus Managed Identity` section that states production uses the Container App managed identity and requires the least-privilege Service Bus RBAC needed to send active queue messages, receive/settle active queue messages, and send safe-DLQ messages.
2. Retain connection-string/SAS wording only as an explicitly local or emergency-rollback path.
3. Add `SERVICEBUS_AUTH_MODE` and `SERVICEBUS_NAMESPACE_FQDN` to the `docker run` environment forwarding list. Keep `SERVICEBUS_CONNECTION_STRING` as the fallback variable because it is harmless when managed identity mode is selected and supports local execution.
4. Adjust the Key Vault example so `KEY_VAULT_SERVICEBUS_CONNECTION_STRING_NAME` is identified as connection-string-mode-only rather than appearing as a mandatory production setting.
5. Run the documentation scan from the original Slice 10A plan and verify the README has no instruction to use SAS credentials for managed-identity production.

### Task 2: Correct the Handover and Whitespace Result [completed in the worktree]

**Files:**
- Modify: `internal/servicebusqueue/deadletter_test.go`
- Modify: `docs/2026-07-12-slice-10a-servicebus-managed-identity-handover.md`

**Source verified:** `git diff --check main...HEAD` currently reports a new blank line at the end of `internal/servicebusqueue/deadletter_test.go`. The handover incorrectly states that the committed branch whitespace check passed, then separately describes the same failure.

1. Remove the extra trailing blank line from `internal/servicebusqueue/deadletter_test.go` without changing test behavior.
2. Update the handover's verification section to state that the original branch check failed because of that whitespace finding, rather than reporting it as passed.
3. Update the handover's current-worktree-state text so it distinguishes the pre-existing uncommitted README from the newly added handover/finalization documents.
4. Run `git diff --check main` and `git diff --check`; both must produce no whitespace findings for the effective worktree. After committing, run `git diff --check main...HEAD` to confirm committed history is also clean.

### Task 3: Final Verification [commands completed; committed-branch whitespace recheck pending commit]

**Files:**
- Verify: `README.md`
- Verify: `internal/config`
- Verify: `internal/secretloader`
- Verify: `internal/servicebusqueue`
- Verify: `internal/app`

**Source verified:** The original Slice 10A plan requires focused tests, the full Go suite, `go vet`, a Service Bus credential leak scan, and a scope check. The repository CI runs `go test ./...` and `go vet ./...`.

1. Run `go test ./internal/config ./internal/secretloader ./internal/servicebusqueue ./internal/app`.
2. Run `go test ./...` and `go vet ./...`.
3. Run `rg -n "SERVICEBUS_CONNECTION_STRING=.*SharedAccessKey|SharedAccessKey=|RootManageSharedAccessKey|servicebus-conn-str" --glob '!docs/superpowers/plans/**'` and inspect each match to confirm it is a redacted example, test data, or secret name rather than a credential.
4. Run `git diff --check main`, `git diff --check`, and `git diff --name-only main`; confirm no Terraform or Azure resource files changed. After committing, rerun the equivalent `main...HEAD` commands.
5. Record the exact commands and pass/fail results in the handover.

### Task 4: Close the Slice Planning Record [completed]

**Files:**
- Modify: `docs/superpowers/plans/README.md`
- Modify: `docs/superpowers/plans/roadmap.md`
- Move: `docs/superpowers/plans/active/2026-07-03-slice-10a-servicebus-managed-identity.md`
- Move: this finalization plan to `docs/superpowers/plans/completed/`

**Source verified:** The planning index currently identifies Slice 10A as the active detailed plan, and the roadmap marks it Active. The requested finalization is limited to the application-side story; Slice 10 Infrastructure remains not planned.

1. Only after Task 3 passes, move both Slice 10A plans to `completed/` and mark their headers `Completed`.
2. Update the planning index and roadmap so neither claims an active Slice 10A plan.
3. Record that application-side managed identity support is complete, while Terraform, managed-identity assignment, Service Bus RBAC provisioning, and live Azure staging validation remain Slice 10 Infrastructure or production-readiness work.
4. Do not modify the Slice 10 Infrastructure draft as part of this finalization; its refresh belongs to that slice's promotion work.

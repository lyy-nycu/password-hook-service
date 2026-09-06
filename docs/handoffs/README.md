# Project Handoffs

Use this directory for dated operational handoffs. Read the newest current
handoff first; historical handoffs preserve context but must not override newer
observed state.

## Current

- [`2026-09-06-e2e-rerun-resolution.md`](./2026-09-06-e2e-rerun-resolution.md)
  — an autopilot E2E-rerun session (create one synthetic Entra account +
  credentials file for manual login testing) was briefly interrupted
  mid-execution with temporary staging changes left live/open, then resumed
  and **completed successfully**: a real signed request was sent (after
  discovering and fixing a flaky single-IP AGW-address assumption), the
  synthetic account was confirmed created via Graph, and all temporary
  staging changes (WAF allowlist, `portal_allowed_cidrs`, `TF_APPLY_MODE`,
  the Key Vault role grant, the hub test VM) were reverted/verified back to
  baseline. The new account and its credentials file were intentionally
  kept (not purged) for manual testing.
- [`2026-08-06-staging-shared-network-remote-plan.md`](./2026-08-06-staging-shared-network-remote-plan.md)
  — shared-network remote-plan evidence, external Network-team gate, and
  on-premises test sequence. The shared-network peering apply (2026-08-12)
  and the application CD apply (2026-09-01/02, source-IP trust-boundary fix
  plus full staging API/E2E validation) have both occurred; the real
  on-premises portal-source acceptance test has not.

## Historical

- [`2026-07-28-staging-network-and-deployment.md`](./2026-07-28-staging-network-and-deployment.md)
  — superseded by the 2026-08-06 remote-plan handoff after the authoritative
  shared-network stack and remote state were established.
- [`2026-07-21-slice-10-to-slice-11.md`](./2026-07-21-slice-10-to-slice-11.md)
  — Slice 10-to-11 transition context. Superseded after Slice 11 CI/CD was
  implemented and merged.

When adding a newer handoff, update this index and mark the previous current
handoff historical or superseded. Do not record secrets, credentials, raw
password values, request bodies, queue payloads, signatures, tokens, or
unredacted sensitive logs in a handoff.

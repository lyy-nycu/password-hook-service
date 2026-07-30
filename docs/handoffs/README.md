# Project Handoffs

Use this directory for dated operational handoffs. Read the newest current
handoff first; historical handoffs preserve context but must not override newer
observed state.

## Current

- [`2026-07-28-staging-network-and-deployment.md`](./2026-07-28-staging-network-and-deployment.md)
  — current staging network, CD plan, and deployment handoff. Azure network
  preparation and the controlled staging apply remain outstanding.

## Historical

- [`2026-07-21-slice-10-to-slice-11.md`](./2026-07-21-slice-10-to-slice-11.md)
  — Slice 10-to-11 transition context. Superseded after Slice 11 CI/CD was
  implemented and merged.

When adding a newer handoff, update this index and mark the previous current
handoff historical or superseded. Do not record secrets, credentials, raw
password values, request bodies, queue payloads, signatures, tokens, or
unredacted sensitive logs in a handoff.

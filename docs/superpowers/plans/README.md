# Implementation Plans

This directory separates executable plans from historical references so agents do not accidentally follow stale instructions.

## Read Order For Agents

1. Read this file.
2. Read `roadmap.md`.
3. Execute only plans in `active/` unless the user explicitly says otherwise.
4. Use `drafts/` only for future-slice planning and research. Do not execute draft plans until they are refreshed and promoted to `active/`.
5. Use `completed/` only as historical context or implementation pattern reference.
6. Do not execute plans in `superseded/`; follow the replacement listed in that plan header.

## Directory Meaning

| Directory | Meaning | Agent Behavior |
|---|---|---|
| `active/` | Current executable implementation plans | Read and execute when asked to implement the active slice |
| `drafts/` | Future-slice research and draft plans that may be stale or blocked by active-slice changes | Read for planning context only; do not execute or treat as current requirements |
| `completed/` | Finished slice plans and historical implementation notes | Reference only; do not treat as current requirements |
| `superseded/` | Plans replaced by newer decisions or plans | Do not execute; read only to understand history |
| `roadmap.md` | Slice status and active-plan pointer | Read before choosing any detailed plan |

## Current Active Plan

No detailed plan is currently active. Slice 10A Service Bus Managed Identity is complete; see `completed/2026-07-03-slice-10a-servicebus-managed-identity.md` and its finalization record.

## Status Labels

Use these labels at the top of detailed plan files:

- **Active:** executable now.
- **Draft:** planning artifact only; not executable until refreshed and promoted to `active/`.
- **Completed:** finished; historical reference only.
- **Completed / Partially Superseded:** finished, but specific assumptions were replaced by a newer plan.
- **Superseded:** not executable; replaced by another plan.

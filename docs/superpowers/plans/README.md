# Implementation Plans

This directory separates executable plans from historical references so agents do not accidentally follow stale instructions.

## Read Order For Agents

1. Read this file.
2. Read `roadmap.md`.
3. Execute only plans in `active/` unless the user explicitly says otherwise.
4. Use `completed/` only as historical context or implementation pattern reference.
5. Do not execute plans in `superseded/`; follow the replacement listed in that plan header.

## Directory Meaning

| Directory | Meaning | Agent Behavior |
|---|---|---|
| `active/` | Current executable implementation plans | Read and execute when asked to implement the active slice |
| `completed/` | Finished slice plans and historical implementation notes | Reference only; do not treat as current requirements |
| `superseded/` | Plans replaced by newer decisions or plans | Do not execute; read only to understand history |
| `roadmap.md` | Slice status and active-plan pointer | Read before choosing any detailed plan |

## Current Active Plan

- `active/2026-07-02-worker-plaintext-lifetime-fix.md`

This follow-up must complete before continuing to the Microsoft Graph slice because the worker still converts decrypted password bytes into an immutable string during processing.

## Status Labels

Use these labels at the top of detailed plan files:

- **Active:** executable now.
- **Completed:** finished; historical reference only.
- **Completed / Partially Superseded:** finished, but specific assumptions were replaced by a newer plan.
- **Superseded:** not executable; replaced by another plan.

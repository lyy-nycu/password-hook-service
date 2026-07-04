# Agent Instructions

## Pull Requests

- Before creating or updating a GitHub pull request, read `.github/pull_request_template.md`.
- Structure the pull request body according to that template. Do not invent a custom PR description format unless the user explicitly asks for one.
- Preserve the template section headings and checklist items unless the user asks to change the repository template.
- Follow the template's security guidance: do not include secrets, credentials, raw password values, queue payloads, request bodies, logs containing sensitive data, or screenshots with sensitive data in pull request text.
- Call out any changes to encryption, queue payloads, DLQ behavior, logging, or configuration in the pull request body.

## Writing Implementation Plans

- When writing implementation plans (e.g. `docs/superpowers/plans/active/`), do not draft an entire multi-task plan with embedded Go code in one pass from memory.
- Verify against the real current source before writing each task: read the actual file(s) it touches, then write that task's section.
- Write and append one task at a time (small, scoped edits), rather than regenerating or rewriting the whole document at once. This avoids oversized single writes that risk context exhaustion, interruption, or hallucinated code.

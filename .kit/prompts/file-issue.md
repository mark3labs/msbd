---
description: File a GitHub issue (bug / feature / docs) with a well-structured body
---

File a GitHub issue for this repository. The user wants to create an issue about: $@

This repo has **no issue templates**, so use bare `gh issue create` and structure the body yourself based on the issue type.

## Steps

1. **Determine the issue type** from the user input: $@
   - **Bug** — something is broken / not behaving as documented → title `fix: ...`
   - **Feature** — new endpoint, flag, env var, or enhancement → title `feat: ...`
   - **Docs** — missing / wrong / unclear documentation → title `docs: ...`

2. **Ask clarifying questions** only if critical info is missing:
   - Bug: "What did you run, and what happened vs. what you expected?"
   - Feature: "What problem does this solve?"
   - Docs: "Where did you look, and what was missing?"

3. **Craft the title** — `<type>: <short description>`, lowercase, imperative, ≤72 chars.
   - Good: `fix: /run returns 500 when sandbox is paused`
   - Good: `feat: add MSBD_LOG_LEVEL env var`
   - Bad: `bug in run` / `it would be nice if...`

4. **Build the body** for the type:

   ### Bug
   - **Description**: what happened vs. expected
   - **Steps to reproduce**: numbered, including the exact request (`curl ...`) and image
   - **Environment**: msbd version (from the startup log line `msbd X.Y.Z (commit ...)`), host (bare metal / VM, nested virt?), `/dev/kvm` present?
   - **Logs / output**: relevant server log lines and the HTTP response body, in code fences

   ### Feature
   - **Description**: what to add/change, specific about behavior
   - **Motivation / use case**: the problem it solves; current workaround and why it's insufficient
   - **Proposed implementation** (optional): which layer it touches (`internal/api` DTO+handler, `internal/core` service method, new route), and whether it changes the wire contract

   ### Docs
   - **Issue**: what's wrong or missing
   - **Location**: file (`README.md`, `openapi.yaml`, `AGENTS.md`) or section
   - **Suggested improvement**: how to fix it

5. **Write the body to a temp file** (`/tmp/issue-body.md`) and create the issue:

       gh issue create --title "<type>: ..." --body-file /tmp/issue-body.md

6. **Confirm success**: show the issue URL and number.

## Guidelines

- Include file paths / line numbers when you know them; use code fences for commands, logs, and responses
- For API bugs, include the endpoint, request body, and the relevant DTO / struct names
- Keep the body factual — avoid speculation except in a clearly-labeled "Proposed implementation" section
- If `gh` is not authenticated (`gh auth status` fails), stop and tell the user
- If a similar issue likely exists, suggest searching (`gh issue list --search "..."`) before filing

$@

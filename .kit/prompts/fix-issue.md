---
description: Implement the fix/feature/docs change requested by a GitHub issue
---

Resolve GitHub issue #$1 by reading it, classifying it, and producing the appropriate code or doc change. **Stop once the working tree contains the change** — committing, pushing, and opening a PR are handled by `/commit-push` and `/create-pr`.

## Steps

1. **Fetch the issue**:
   - Run: gh issue view $1 --json number,title,body,labels,state,author,comments
   - If the issue is closed, stop and ask the user whether to proceed
   - Read the **entire** thread including comments — the latest comment often refines the ask

2. **Classify the issue** from labels, title prefix, and body content:
   - `bug` / `fix:` → reproduce, then fix
   - `enhancement` / `feature` / `feat:` → design, then implement
   - `documentation` / `docs:` → locate and update docs
   - `question` / `discussion` → answer in a comment, do **not** write code
   - Anything else → ask the user how to proceed

3. **Create a working branch** off the default branch:
   - `git checkout main && git pull --ff-only`
   - Branch name: <type>/$1-<slug> (e.g. `fix/42-resolve-after-restart`, `feat/57-logs-endpoint`, `docs/63-readme-host-reqs`)

4. **Do the work** based on type:

   ### Bug (`bug` label / `fix:` title)
   - Reproduce the failure first (write a failing test if feasible) — if you cannot reproduce, comment on the issue asking for clarification and stop
   - Locate the root cause; do not patch symptoms
   - Add or extend a regression test that fails before and passes after the fix
   - Run `task test:race` (or `go test -race ./...`) and `task lint` (or `golangci-lint run`)

   ### Feature (`enhancement` / `feature` label / `feat:` title)
   - Re-read the motivation in the issue body
   - **If it's a new endpoint**, follow the recipe in AGENTS.md "Adding a new endpoint": DTOs in `internal/api/dto.go` → business method in `internal/core/service.go` (all SDK calls stay in `core`) → handler in `internal/api/handlers.go` → route in `internal/api/router.go` (with `s.auth(...)`) → document in `openapi.yaml` → update the README table
   - For large, ambiguous, or breaking changes, sketch the design in a comment on the issue and wait for sign-off before writing code
   - Add godoc on every exported symbol; add unit tests for new behaviour and edge cases
   - Keep `internal/api/dto.go` and `openapi.yaml` in lockstep; no `omitempty` on input fields
   - Run `task test:race` and `task lint`; run `nix flake check` if you touched `flake.nix`/`nix/`

   ### Documentation (`documentation` label / `docs:` title)
   - Update the relevant surface: `README.md`, `AGENTS.md`, `openapi.yaml`, or godoc
   - Verify code samples compile (`go build ./...`)
   - Run `task lint` if Go files were touched

5. **Report**:
   - Branch name (`git branch --show-current`)
   - Summary of files changed (`git status -s`) and the diff highlights
   - Test/lint results (pass/fail with key output)
   - Suggest the next step explicitly:
     - `/commit-push` to commit with a Conventional Commit subject (reference `(#$1)` and include `Fixes #$1` so merge auto-closes)
     - then `/create-pr $1` to open the pull request

## Guidelines

- This prompt **stops at a clean working tree with the change applied** — do not run `git commit`, `git push`, or `gh pr create`
- If the issue is unclear, post a clarifying comment on the issue and stop; do not guess
- Keep the change scoped to the issue; surface unrelated cleanups separately
- Respect the boundaries: the SDK only gets called from `internal/core`; api never imports it
- Microsandbox integration paths need `/dev/kvm` and aren't run in CI — if a change can only be verified by booting a microVM, note that the test is gated behind `-tags integration` and say what manual verification is needed
- Do not close the issue manually — the eventual PR's `Fixes #$1` handles that on merge

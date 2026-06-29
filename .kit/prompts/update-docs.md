---
description: Audit and update project documentation for a recent change
---

Review recent code changes, identify all documentation surfaces that should
mention them, and update each one — grounded in the actual diff, not guesses.

## Steps

1. **Identify the change**:
   - If the user input ($@) names a commit / PR / branch / topic, use that as the focus
   - Otherwise inspect `git log origin/main..HEAD --oneline` and `git diff origin/main...HEAD --stat` to discover what shipped on the current branch
   - Read the actual diff (`git diff origin/main...HEAD`) — never document features that aren't in the code

2. **Inventory the doc surfaces** for this repo:
   - `README.md` — user-facing: the endpoint table, Configuration env-var table, Quickstart, Nix, host requirements
   - `openapi.yaml` — the wire contract: schemas under `components/schemas`, response examples, error envelopes. **Any DTO change MUST be reflected here.**
   - `AGENTS.md` — contributor-facing conventions, gotchas, "Adding a new endpoint", layout map
   - Inline godoc on new/changed exported symbols in `internal/...` and `cmd/msbd/`
   - `Taskfile.yml` task descriptions, if a workflow changed

3. **Audit each surface** with `grep`:
   - If an endpoint was added/changed, grep `README.md` and `openapi.yaml` for the sibling endpoints to find the right place and matching style
   - If an env var was added, check the Configuration table in `README.md` and `loadConfig` in `cmd/msbd/main.go`
   - If a DTO field changed, cross-check `internal/api/dto.go` against `openapi.yaml` — they must agree
   - Decide for each hit: update, cross-reference, or leave untouched

4. **Decide where new content lives**:
   - Prefer extending an existing section/table over creating a new one
   - Place new endpoint rows in the README API table and matching paths/schemas in `openapi.yaml`
   - Skip surfaces that genuinely don't apply and say so explicitly

5. **Draft the updates**:
   - Lead with a one-sentence statement of what's new and why
   - Show concrete examples copied from real signatures / DTOs — verify against the source files
   - Keep `internal/api/dto.go` ⇄ `openapi.yaml` ⇄ README table consistent
   - Note backward-compatibility (additive vs breaking) where relevant

6. **Verify before committing**:
   - `go build ./...` to confirm code samples / godoc references compile
   - If `openapi.yaml` changed, sanity-check it parses (e.g. a YAML lint or `python3 -c 'import yaml,sys; yaml.safe_load(open("openapi.yaml"))'`)
   - `go vet ./...` and `go doc <pkg> <Symbol>` to sanity-check godoc rendering

7. **Report**:
   - List every file changed and every file deliberately left alone (with a one-line reason)
   - Suggest the next step (typically `/commit-push`) — do not auto-commit unless asked

## Guidelines

- Read the diff before writing anything — invented field/endpoint names erode trust faster than missing docs
- The three-way contract (`dto.go` ⇄ `openapi.yaml` ⇄ README table) is the #1 thing to keep in sync
- Keep doc updates separate from code changes when possible
- Match the existing voice and formatting of each surface (headings, code-fence languages, table styles)

$@

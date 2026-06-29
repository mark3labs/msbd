---
description: Read-only audit for dead code, duplication, boundary violations, and refactor opportunities
---

Perform a comprehensive **read-only** audit of this repository and report
findings. **Do not edit, rename, or delete any files.** Optional focus / scope
hints from the user: $@

## Scope

If the user supplied focus hints above (a package path, a concern like "jobs"
or "registry"), scope the audit accordingly. Otherwise audit the whole repo,
prioritising the highest-traffic packages first (`internal/core/`,
`internal/api/`, `cmd/msbd/`).

## Steps

1. **Map the repo first**:
   - `ls` / `find` the top-level layout and list every Go package
   - Read `AGENTS.md`, `README.md`, and `openapi.yaml` to understand the
     intended architectural boundaries and invariants
   - The load-bearing invariants for this repo (from AGENTS.md):
     - **`internal/api` MUST NOT import the microsandbox SDK.** All
       `github.com/superradcompany/microsandbox/sdk/go` references stay in
       `internal/core/` (the cgo isolation boundary).
     - **All `*msb.Sandbox` access goes through `Registry.resolve()`** — never
       grab a handle from the cache map directly.
     - **`internal/api/dto.go` is the wire contract** — it must stay in lockstep
       with `openapi.yaml`; no `omitempty` on REST input fields; renamed JSON
       fields are breaking.
     - **Every sandbox is created `WithDetached()`** so it survives a restart.
     - Errors from `core` are typed (`ErrNotFound`), never HTTP statuses.

2. **Hunt for dead code**:
   - Run `go vet ./...` and capture warnings
   - Use `grep` to find exported symbols (`^func [A-Z]`, `^type [A-Z]`,
     `^var [A-Z]`, `^const [A-Z]`) and cross-reference call sites. Symbols
     with zero non-test references inside the module are suspects
   - Check for unreferenced files, `// TODO: remove` markers, commented-out
     blocks, and `_ = x` discard patterns
   - If `staticcheck`, `deadcode`, or `unused` are available on PATH, run
     them and include their output verbatim
   - **Do not delete anything** — list candidates with file:line and a
     confidence level (high / medium / low)

3. **Find unnecessary duplication**:
   - Look for near-identical function bodies, struct shapes, or handler
     patterns across `internal/api` and `internal/core` — `grep` for repeated
     signatures and copy-pasted error strings is a fast first pass
   - Distinguish *coincidental* duplication from *unnecessary* duplication
     (same intent, drifting in lockstep) — only flag the latter
   - For each cluster, propose where the extracted helper should live, and
     whether moving it would cross the api↔core boundary

4. **Check boundary violations**:
   - **SDK leakage**: grep `internal/api/` for any import of
     `superradcompany/microsandbox` — there should be zero
   - **resolve() bypass**: grep `internal/core/` for direct reads of the
     registry handle map instead of going through `resolve()`
   - **DTO ↔ OpenAPI drift**: cross-check the fields in `internal/api/dto.go`
     against the schemas in `openapi.yaml`; flag any field present in one but
     not the other, and any `omitempty` on an input field
   - **Detached invariant**: grep sandbox-create paths for a missing
     `WithDetached()`
   - **HTTP status leakage**: `core` returning HTTP codes instead of typed errors
   - For each violation, cite the offending import / signature with file:line

5. **Spot refactor opportunities**:
   - Long functions (>80 lines) doing multiple unrelated things
   - Deeply nested conditionals that flatten well with early returns
   - Repeated `if err != nil { return fmt.Errorf("...: %w", err) }` chains
     that could become helpers — only where the wrapping context is uniform
   - Handlers that don't follow the documented `decode → svc.X → encode |
     notFoundOr` shape
   - Flag each with: location, current shape (1-2 lines), proposed shape
     (1-2 lines), and estimated risk (low / medium / high)

6. **Cross-check against project rules**:
   - Re-read AGENTS.md "Conventions & gotchas" and verify nothing in your
     findings contradicts a documented gotcha (e.g. a "refactor" that would
     put a low timeout in front of `/run`, or make `Exec` ensure-running) — if
     a suggestion would reintroduce a known pitfall, drop it and note why

7. **Write the report** as your final message (do not write it to disk),
   structured as:

   ```
   # Code Audit Report

   ## Summary
   - N dead-code candidates
   - N duplication clusters
   - N boundary violations
   - N refactor opportunities

   ## Dead Code
   ### High confidence
   - path/to/file.go:LINE — symbol — reason

   ## Duplication
   ### Cluster: <short name>
   - Sites: file:line, file:line, …
   - Suggested home: package/path

   ## Boundary Violations
   - Rule: <which invariant from AGENTS.md>
   - Offender: file:line
   - Fix sketch: …

   ## Refactor Opportunities
   - Location: file:line
   - Current / Proposed / Risk / Why it's worth it

   ## Suggested Next Steps
   1. …
   ```

8. **End with an explicit reminder** that no files were modified, and
   recommend the user act on the highest-leverage items manually (or via a
   follow-up `/fix-issue`) rather than a sweeping refactor.

## Guidelines

- **Read-only, always**: no `edit`, no `write`, no `git commit`. Use only
  `read`, `grep`, `find`, `ls`, and read-only `bash` (`go vet`,
  `go build -o /tmp/...`, `staticcheck`)
- **Cite every finding** with `path/to/file.go:LINE`
- **Be honest about confidence** — prefer "medium, worth a look" over
  confidently wrong
- **Quantity isn't quality**: 10 sharp findings beat 100 nitpicks
- **Skip vendored / generated code**
- **Don't propose architectural rewrites** — stay within the existing shape
  and recommend incremental, reviewable changes

---
description: Semantic version tagging workflow — analyzes commits and cuts a release via the VERSION file + Taskfile
---

# Release Tagging Workflow

Cut a new release of msbd following semantic versioning.

msbd's version is **driven by the `VERSION` file**, not by a raw `git tag`. The
git tag is the source of truth for the published version, but the `VERSION` file
is what ties a Nix build to that number (flakes can't read git tags). The two
MUST agree — `task release` bumps them atomically and a CI guard fails the
release on mismatch. Always go through the task; never `git tag` by hand.

## Steps

1. **Fetch remote tags**: `git fetch --tags origin`

2. **Find latest version**: `git tag -l | sort -V | tail -5` to see recent tags.
   Cross-check against the working tree: `cat VERSION`.

3. **Analyze changes since last tag**:
   - `git log <latest-tag>..HEAD --oneline` — list commits
   - `git diff <latest-tag>..HEAD --stat` — see file stats
   - `git diff <latest-tag>..HEAD --name-only` — see changed files

4. **Determine version bump** (Semantic Versioning):
   - **MAJOR (X.0.0)**: Breaking changes to the REST API or wire contract.
   - **MINOR (0.X.0)**: New features, backward-compatible additions.
   - **PATCH (0.0.X)**: Bug fixes, backward-compatible fixes.

   Look for indicators:
   - `feat:` commits → MINOR
   - `fix:` commits → PATCH
   - `breaking:` or `BREAKING CHANGE:` → MAJOR
   - Changes to `internal/api/dto.go`, `openapi.yaml`, route paths, or removed/
     renamed JSON fields → MAJOR (the DTOs are the wire contract — see AGENTS.md;
     a renamed JSON field is breaking for every downstream client).
   - New endpoints, flags, or env vars → MINOR
   - Documentation-only / internal refactors → PATCH (or skip)

5. **Calculate new version**: Increment the appropriate segment, reset lower
   segments to 0. The next version is `X.Y.Z` (no leading `v`).

6. **Pre-flight checks** (the `release` task enforces these, but verify first so
   you don't fail late):
   - Working tree is clean: `git status --porcelain` is empty.
   - `NEW_VERSION` is valid semver and greater than `cat VERSION`.
   - Tag `vX.Y.Z` does not already exist.
   - Tests pass: `task test` (CI runs `-race`; `task test:race` to match).

7. **Draft the change summary** for confirmation:
   - Summarize key changes from commits.
   - Group by type (Features, Fixes, Breaking Changes).
   - Call out any wire-contract / OpenAPI changes explicitly.

8. **Cut the release** (after the user confirms version + summary):

   ```bash
   # Bumps VERSION, commits "release: vX.Y.Z", creates the tag, and pushes.
   task release:push NEW_VERSION=X.Y.Z
   ```

   Or, to inspect before pushing:

   ```bash
   task release NEW_VERSION=X.Y.Z       # local bump + commit + tag only
   git show HEAD; git show vX.Y.Z       # review
   git push origin HEAD vX.Y.Z          # then push
   ```

9. **What happens next** (no manual action — just confirm it ran):
   - The push triggers `.github/workflows/release.yml`.
   - CI verifies `v$(cat VERSION)` equals the pushed tag (fails on mismatch).
   - GoReleaser builds linux/amd64 + linux/arm64 binaries (CGO), pushes
     multi-arch Docker images to `ghcr.io/mark3labs/msbd`, and publishes a
     GitHub release with the rendered changelog.
   - The Nix flake reads the same number from `VERSION`, so `nix build` off the
     tag reports an identical version.

## Guidelines

- **Always release through `task release` / `task release:push`.** Hand-tagging
  skips the VERSION bump and the CI guard will reject the release.
- Always fetch remote tags first to avoid conflicts.
- Follow semver strictly — when in doubt, prefer the conservative bump
  (patch over minor). Treat any DTO / `openapi.yaml` change as potentially
  breaking until proven backward-compatible.
- If there are no changes since the last tag, suggest skipping the release.
- The `release` task creates a lightweight tag plus a `release:` commit; the
  commit message + the GoReleaser changelog carry the human-readable summary.
- Wait for the user to confirm the version and summary before running the tag
  commands.

## Example confirmation summary

```
Proposed release: v0.2.0 (MINOR)

Features:
- Add /v1/sandboxes/{id}/logs endpoint
- New MSBD_LOG_LEVEL env var

Fixes:
- Reconnect-after-restart no longer drops cached workdir

Wire contract: additive only (new optional fields), backward-compatible.
```

---

$@

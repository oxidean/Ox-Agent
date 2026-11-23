# Plan: repository-change evidence for spec coverage

## Stage 1 — Capture the work-item baseline

- Add the optional `git_baseline` field to `state.WorkItem` and its canonical
  schema.
- Capture a validated `HEAD` before creating the work-item directory/event,
  only when the Git worktree is clean; otherwise keep it unavailable.
- Add tests for clean Git, dirty/staged/untracked, non-Git, and no-commit starts,
  plus loading indexes without the optional field.

## Stage 2 — Derive repository changes safely

- Read tracked changes from the baseline with NUL-delimited name-status output
  and add non-ignored untracked paths.
- Include deletion and rename source/destination paths; normalize and sort.
- Exclude only `.ox-agent/work/`, `.ox-agent/state/`, and `.ox-agent/runtime/`
  from coverage evidence. Validate the stored hash before Git invocation.
- Add tests for staged, unstaged, committed, untracked, delete/rename, malformed
  baseline, and excluded paths.

## Stage 3 — Enforce evidence against covered specs

- Match actual changed paths against valid `covers:` globs.
- Emit `underreported` when a covered changed path is absent from declared
  artifacts, and `unacknowledged` when the covered spec is not consulted.
- Emit `evidence_unavailable` per covered spec when the baseline or Git query
  is unavailable; preserve warn journaling and strict durable-decision behavior.
- Update warning rendering, the promoted coverage spec, canonical/materialized
  schema assets, and CLI/architecture documentation.
- Add warn/strict/off and legacy-item tests; keep `off` from invoking Git.

## Stage 4 — Verify and prepare delivery

- Run focused tests, full Go tests, vet, build, asset checks, docs-manifest
  validation, and a diff review.
- Dogfood the behavior in a clean temporary Git repository and in an item with
  unavailable evidence; then record review and ship readiness.

## Gates and risks

Stage 2 depends on Stage 1. Strict mode must not silently pass a missing
baseline; the existing durable decision flow is the resolution point. A legacy
item cannot recover its true historical start commit, so it stays unavailable
rather than being assigned the current HEAD. Git path parsing must remain
NUL-safe, and rename paths are filtered individually by the explicit exclusions.

---
covers:
  - internal/repochange/**
  - internal/project/**
  - internal/state/**
  - internal/harness/**
  - schemas/work-item.schema.json
  - docs/cli.md
  - docs/architecture.md
  - docs/site/architecture.mdx
  - docs/site/cli.mdx
  - .ox-agent/context/specs/README.md
---

# Spec: repository-change evidence for spec coverage

## Goal

Make `covers:` enforcement compare the agent's declared artifacts with actual
repository changes, so a covered file omitted from `output.artifacts` cannot
silently bypass reconciliation.

## Work-item baseline

- Add optional `git_baseline` commit ID to work-item state and its schema.
- At `ox-agent start`, before writing the new work item or journal event,
  capture `HEAD` only when the project is a Git worktree, has a valid commit,
  and `git status --porcelain --untracked-files=all` is empty.
- If the project is not Git, has no commit, has staged/unstaged changes or
  non-ignored untracked files, or Git inspection fails, leave the baseline
  unavailable. Starting a work item still succeeds.
- Document that users need a clean worktree, including a committed Ox-Agent
  event journal, to obtain a baseline; dirty starts cannot provide evidence.
- Existing work items without this field remain unavailable; do not infer or
  backfill a historical baseline.

## Repository change set

For a work item with a valid baseline, derive a deterministic project-relative
path set from:

- `git diff --relative --name-status -z --find-renames <baseline> --`, covering
  committed, staged, unstaged, and deleted tracked paths;
- `git ls-files --others --exclude-standard -z`, covering non-ignored untracked
  files.

The paths must be relative to the Ox-Agent project root so they match
`covers:` globs and artifact paths, even when that root is nested inside a
larger Git worktree.

Include both old and new paths for renames. Exclude `.ox-agent/work/`,
`.ox-agent/state/`, and `.ox-agent/runtime/` as Ox-Agent work/journal/runtime
control-plane data. Git's own commands exclude `.git/` metadata and ignored
untracked output. Do not exclude tracked generated files or other paths merely
because they are hidden/generated; a matching `covers:` glob governs them.

## Coverage behavior

- Continue loading and validating `covers:` from the spec files and preserve
  the existing policy modes.
- Use actual repository changes, not declared artifacts alone, as the paths
  tested against `covers:`.
- For a covered changed path absent from the output's declared artifacts,
  create an `underreported` finding containing the spec and sorted paths.
- For covered changed paths where the spec is not in `spec_impact.consulted`,
  create the existing unacknowledged-spec finding using actual paths.
- If no valid baseline or Git evidence is unavailable while covered specs
  exist, create an explicit `evidence_unavailable` finding for each such spec;
  do not silently fall back to the declared artifact list.
- Under `warn`, findings are journaled as `spec.drift` and surfaced as warnings.
  Under `strict`, they use the existing durable decision hold. Under `off`,
  spec evaluation remains disabled.
- An accepted `evidence_unavailable` decision releases only that run; future
  runs without repository evidence still require a decision.
- Preserve deterministic finding/path ordering and use the existing event and
  input-request formats; no new policy setting is introduced.

## Compatibility and non-goals

- Older work-item indexes remain readable; `git_baseline` is optional.
- Git commands use argument arrays, never a shell, and commit IDs are validated
  before use.
- Do not infer a baseline from the current checkout for an existing item.
- Do not add an automatic migration, baseline-reset command, or a blanket
  exclusion for generated source files.
- Do not change generic lifecycle state or silently mutate request state.

## Acceptance criteria

1. A clean Git worktree with a commit records that commit as the new item's
   baseline before work-item state is written.
2. Non-Git, uncommitted, untracked, or no-commit start conditions leave the
   baseline unavailable without failing `start`.
3. Coverage detects project-relative tracked staged/unstaged/committed changes
   and non-ignored untracked files made after the baseline; deleted paths and
   both rename sides are included. This also works when the project is nested
   below the Git worktree root.
4. A covered changed file omitted from declared artifacts is journaled as an
   `underreported` finding; matching actual paths omitted from `consulted`
   remain unacknowledged.
5. Git evidence failure or missing legacy baseline is explicitly reported and
   never replaced by an artifact-only pass.
6. `warn` journals/surfaces findings, `strict` holds on them through the
   existing durable decision request, and `off` does not inspect Git.
7. `.ox-agent/work/`, `.ox-agent/state/`, and `.ox-agent/runtime/` are excluded;
   tracked generated source remains subject to matching coverage globs.
8. Tests cover clean/dirty/non-Git baselines, missing baseline, tracked and
   untracked changes, deleted/renamed files, exclusions, warning/strict behavior,
   deterministic results, and legacy item compatibility.
9. CLI/architecture docs explain the baseline and unavailable-evidence policy.
10. Init and repair seed a specs README that describes baseline-backed
    coverage and the clean-worktree requirement.

## Verification

Run focused state and harness tests, `go test ./...`, `go vet ./...`,
`go build ./cmd/ox-agent`, `scripts/check-assets.sh`, and
`docs-manifest validate docs/site`.

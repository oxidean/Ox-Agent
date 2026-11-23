# Discovery: repository-change coverage evidence

## Existing behavior

- `internal/harness/specs.go` evaluates `covers:` using the skill's declared
  `output.artifacts`; a covered path omitted from `spec_impact.consulted` is
  reported, but an omitted path in `output.artifacts` is invisible.
- Strict policy already turns findings into durable spec-drift decisions and
  fails closed when coverage metadata itself is unreadable.
- `state.WorkItem` records lifecycle metadata but no repository baseline.
- Work-item state is written under `.ox-agent/work/`; the root `.gitignore`
  excludes `.ox-agent/runtime/`, not tracked work artifacts.

## Design constraints

- A baseline must be captured before `Start` writes its new item/event. Use the
  current Git commit only when the repository is available and the worktree has
  no staged, unstaged, or non-ignored untracked changes; otherwise record that
  evidence is unavailable rather than attributing existing edits to this item.
- Compare current tracked state to the recorded commit and add non-ignored
  untracked paths. Include both source and destination for renames/deletions.
- Do not blanket-exclude generated or `.ox-agent` paths from coverage: a spec's
  `covers:` glob is the authority. Git-ignored files and `.git/` are outside the
  evidence set by the Git commands.
- Missing baseline or Git failure produces an explicit evidence-unavailable
  finding when covered specs exist. Warn mode journals/surfaces it; strict mode
  uses the existing durable decision hold. Never silently fall back to the
  declared artifact list.

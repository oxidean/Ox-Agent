# context/specs — promoted specifications

`work/` is the full trail: every work item keeps its own `spec.md`,
`plan.md`, and artifacts through `work/archived/`. This directory is
the curated layer — specifications promoted out of finished work because they
should inform future work, plus spec docsets migrated from before the project
ran ox-agent — keep their originals outside `context/` (the archived work
item is provenance enough) so they don't index as specs themselves.

Promote a finished item's spec with `ox-agent context promote <wi>` — it
copies the work item's `spec.md` here, refreshes the index, and journals
the promotion. A file here is the spec of record for its subject. Promotion
always copies from the work item: amend the item's `spec.md` and re-promote,
or edit this file directly (skills do that and record it in
`spec_impact.updates`) — editing here then re-promoting loses the edit to
the item's older copy. Never let the record go stale behind the
implementation's back.

Specs may declare `covers:` front-matter globs naming the repo paths they
govern. Collection compares repository changes since the work-item-start Git
baseline against those globs and the run's declared artifacts, and reports
underreported or unacknowledged spec-governed changes. If no clean Git
baseline or repository evidence is available, warn mode reports that fact and
strict mode holds for a durable decision; it never silently falls back to the
agent-declared artifact list. `.ox-agent/work/`, `.ox-agent/state/`, and
`.ox-agent/runtime/` are excluded from this comparison.
Changed paths are relative to the project root, even when it is nested inside
a larger Git worktree. Warnings and durable decisions name the affected spec
and evidence problem.

To obtain a baseline, start from a clean Git worktree. Commit project changes,
including Ox-Agent's event journal, before starting a new work item. A dirty
tree leaves evidence unavailable: warn mode reports that fact, and strict
mode holds covered outcomes for a decision.

`ox-agent context index` classifies files here as `spec`, and packet
compilation hands them to skills under `context.specs`. Everything here is
meant to be committed.

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
govern — collection intersects a run's declared artifacts against them and
reports spec-governed changes that never consulted the spec.

`ox-agent context index` classifies files here as `spec`, and packet
compilation hands them to skills under `context.specs`. Everything here is
meant to be committed.

# context/specs — promoted specifications

`work/` is the full trail: every work item keeps its own `spec.md`,
`plan.md`, and artifacts through `work/archived/`. This directory is
the curated layer — specifications promoted out of finished work because they
should inform future work, plus spec docsets migrated from before the project
ran ox-agent — keep their originals outside `context/` (the archived work
item is provenance enough) so they don't index as specs themselves.

`ox-agent context index` classifies files here as `spec`, and packet
compilation hands them to skills under `context.specs`. Everything here is
meant to be committed.

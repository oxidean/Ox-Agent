# Brainstorm: repository-change coverage

The current gate compares `covers:` with `output.artifacts`. That only verifies
what the agent chose to report, so omitted changed files can escape review.
Use Git as independent repository evidence, tied to the work item rather than
to a particular run.

## Decisions

- **Baseline:** capture `HEAD` when `ox-agent start` runs, only if the project is
a Git worktree with a commit and a clean index/worktree, including
non-ignored untracked files. If any prerequisite is missing, keep the baseline
unavailable; never guess or retroactively backfill it.
- **Change set:** tracked staged and unstaged changes from the baseline plus
non-ignored untracked files. Include deleted paths and both sides of renames.
Tracked generated files count when covered; ignored untracked build output does
not. Exclude `.ox-agent/work/`, `.ox-agent/state/`, and `.ox-agent/runtime/`
from this comparison as Ox-Agent's work/journal/runtime control plane; do not
blanket-exclude other hidden or generated source paths.
- **Missing evidence:** no-Git, dirty-at-start, invalid/missing baseline, or Git
inspection failures produce an explicit evidence-unavailable finding for each
valid covered spec. Warn mode journals and surfaces the finding; strict mode
uses the existing durable decision hold; off mode still skips spec evaluation.
There is no fallback to declared artifacts as a substitute for missing Git
evidence.
- **Legacy items:** missing baseline remains unavailable. Do not infer an old
start commit or mutate existing work items during migration.
- **Comparison:** actual changed paths are the source for coverage. A covered
changed path absent from `output.artifacts` is an underreported-change finding.
A covered changed path whose spec is absent from `spec_impact.consulted` remains
an unacknowledged-spec finding. Deterministically sort/deduplicate evidence.

## Delivery

Implement in a separate PR from the spec-status command. Preserve the existing
warn/strict/off policy and event/decision machinery, add regression tests for
Git states and rename/untracked handling, and document the evidence boundary.

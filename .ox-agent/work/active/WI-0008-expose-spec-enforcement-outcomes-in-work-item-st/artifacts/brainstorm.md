# Brainstorm — spec enforcement observability

## Problem

Strict enforcement can hold a passing outcome and file a durable decision request. The collector also journals `spec.checked` and `spec.drift`. `ox-agent status <WI>` currently shows lifecycle fields and recent event names, so a user has to inspect `events`, `state explain`, and possibly `input show` to understand the spec outcome. The behavior is durable, but not easy to discover.

The governing spec is `context/specs/README.md`: keep promoted specs as the record, commit them, and do not change enforcement semantics silently.

## Option A — concise spec summary in existing status

Show the latest relevant spec-check outcome for the requested work item: consulted specs, drift/unacknowledged findings, and any pending spec-decision request. Read journal events and durable input state; do not rerun coverage matching in the status command. Keep the no-argument status list compact.

**Benefits:** Uses the familiar command; explains why a run is held; reads durable state and avoids a second coverage implementation.

**Costs:** Requires a stable compact rendering contract and careful handling of malformed or missing historical event data.

**Boundary:** Do not expose every event payload in the list view. Detailed history remains in `events` and `state explain`.

## Option B — add a focused `spec status [WI]` command

Create a dedicated view for latest checks, findings, and open decisions, leaving generic status unchanged.

**Benefits:** Keeps general status minimal and creates room for spec-specific detail.

**Costs:** Adds a new CLI surface and duplicates discovery with `status`, `events`, and `input` unless the command has a clearly distinct use.

## Option C — strengthen coverage evidence beyond declared artifacts

Compare declared artifact paths with repository changes, so an agent cannot silently omit a touched covered file from `artifacts`.

**Benefits:** Improves the enforcement trust boundary; addresses under-reporting rather than only observability.

**Costs:** Larger and riskier: must define staged, unstaged, untracked, generated, renamed, and non-git behavior; can create false positives when a worktree contains unrelated changes. This should be a separate work item, not bundled with a status enhancement.

## Decision

Pursue both B and C, in sequence and as separate work items:

1. Implement Option B first: add a focused, read-only `spec status [WI]` command and leave generic `status` unchanged. This gives users a dedicated place to inspect spec-check outcomes before changing enforcement evidence.
2. Plan Option C as a separate follow-up: compare repository changes with spec coverage so enforcement does not rely only on the agent-declared artifact list. Keep this separate because its correctness depends on defining Git baselines and behavior for staged, unstaged, untracked, generated, renamed, and non-Git changes.

The two work items should share the same definition of spec findings and decision state. The status command should report durable results rather than rerunning coverage checks; the later coverage work should produce results that the command can explain.

## Planning questions

- For `spec status`, should the default view show the latest check only, or also retain unresolved findings from earlier checks? Include pending strict-decision request IDs either way.
- For repository-change evidence, what baseline defines changes attributable to a run: the work item's start, the individual run's packet creation, or another explicit snapshot?
- Which generated and Ox-Agent-managed paths should be excluded from coverage matching, and how should renames be attributed?
- In non-Git repositories, should strict coverage fall back to declared artifacts, warn, or block because repository evidence is unavailable?

---
covers:
  - internal/cli/spec.go
  - internal/cli/root.go
  - internal/cli/cli_test.go
  - docs/cli.md
  - docs/site/cli.mdx
---

# Spec: spec enforcement status

## Goal

Give users a focused, read-only command for understanding spec checks and
strict spec decisions without expanding the existing `status` output.

## Command contract

Add `ox-agent spec status [work-item]`.

- With a work-item argument, resolve it using the existing work-item resolver
  and show its spec status, including archived items.
- Without an argument, show spec status for active work items that have spec
  check events or pending spec-decision requests. Omit active items with no
  spec activity.
- Do not change the behavior or output of `ox-agent status`.
- Print through Cobra's configured output writer. Invalid work-item references
  and journal read errors must be returned to the caller.

## Status data

The command reads the append-only event journal only. It must not rerun
coverage evaluation, mutate request state, append events, or alter work-item
state.

- The latest check is the newest `spec.checked` or `spec.drift` event for the
  work item. Show its timestamp, event type, run ID, skill, enforcement mode,
  and consulted spec paths when present.
- Open findings are replayed by spec path. A `spec.drift` event replaces the
  open findings for each spec represented in its findings list. A later
  `spec.checked` event clears open findings for each spec it lists in
  `consulted`. Findings without a spec path remain open until a later clean
  check, which clears unscoped findings.
- Pending spec decisions are `input.requested` events of kind `decision` whose
  question begins `spec drift in <work-item-id>:` and have no later
  `input.answered`, `input.expired`, or `input.cancelled` event for the same
  request ID. Show the request ID and question.
- If the item has no spec checks, open findings, or pending spec decisions,
  report that no spec activity is recorded.
- Preserve finding kind, spec path, summary, and covered paths in the output.
  Sort findings and paths deterministically.

## Compatibility

This is additive. Existing commands, journal event formats, policy behavior,
and persisted state remain unchanged. Older journal entries without optional
spec fields must not cause a panic.

## Non-goals

- Do not change spec enforcement or its finding semantics.
- Do not recompute coverage from repository files.
- Do not add JSON output or mutate, expire, or answer input requests.
- Do not include spec summaries in the generic `status` command.

## Acceptance criteria

1. `ox-agent spec status WI-0001` displays the latest spec check and its
   consulted paths or findings.
2. A later clean check clears findings only for specs it lists as consulted;
   findings for other specs remain visible.
3. Findings without a spec path remain visible until a later clean check.
4. A pending spec-drift decision appears with its request ID and disappears
   after an answer, expiry, or cancellation event.
5. With no argument, only active work items with spec activity are displayed.
6. An item with no spec activity gets a clear no-activity message when queried
   directly.
7. Invalid references and malformed journals return errors; normal status
   output and behavior remain unchanged.
8. The CLI reference documents the new command and its output.
9. Command tests cover clean checks, drift replay/clearing, pending and closed
   decisions, no-argument filtering, and read-only behavior.

## Verification

Run focused CLI tests, then `go test ./...`, `go vet ./...`, and
`go build ./cmd/ox-agent`.

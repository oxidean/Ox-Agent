# Plan: spec enforcement status

## Stage 1 — Project journal state

- Read the journal once and filter `spec.checked` / `spec.drift` events for a
  work item.
- Derive the latest check, replay open findings by spec path, and track pending
  spec-decision request IDs from request/terminal events.
- Keep the projection read-only and deterministic; do not rerun coverage or
  read/write request files.

## Stage 2 — CLI and regression tests

- Add the top-level `spec` command group with `status [work-item]`.
- Render one requested work item, or active items with spec activity when no
  argument is given. Leave `status` unchanged.
- Add in-process CLI tests for latest-check output, finding clear/replay rules,
  pending versus terminal decisions, no-argument filtering, invalid IDs, and
  unchanged journal bytes.

## Stage 3 — Documentation and gates

- Document the command in `docs/cli.md` and `docs/site/cli.mdx`.
- Run focused CLI tests, the full Go suite, vet, build, and docs-manifest
  validation; inspect the final diff and run commit-check.

## Dependencies and risk

Stage 2 depends on the event replay contract from Stage 1. The primary risk is
misclassifying old findings as resolved; the implementation follows the spec's
per-path clearing rule and retains findings when a check does not name their
spec. Input decisions are reconstructed from append-only journal events, so the
command remains read-only and does not depend on request-file expiry side
 effects.

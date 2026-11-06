---
name: ox-agent/implementation
description: Implement one bounded, approved work-item stage while respecting scope, standards, and file constraints.
---

# implementation

Implement one bounded, approved work-item stage while respecting scope, standards, and file constraints.

## Invocation contract

- You run against a work-item packet (JSON): `context` carries the bounded inputs, `constraints` the limits, and `output` declares where to write your result (`output.path`, relative to `.ox-agent/`) and the schema it must satisfy (`output.schema`).
- Write exactly one JSON object to `output.path`. `ox-agent` schema-validates it on collection, journals the outcome, and applies your declared `next_action`.
- `next_action.type` must be one of `advance_stage` (unit of work done — move to the next stage), `retry` (needs another pass — the run does not count as done, so the skill stays dispatchable), `escalate` (blocked — needs a human), or `complete` (stage/pipeline finished). Always include a `reason`.

## Consumes

- the spec/plan artifacts in `context.references` and `constraints.allowed_paths` bounding edits.

## Produces

- code changes listed in `artifacts` as `changed_paths` entries with one-line summaries; failure edges in `findings`.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

## Evaluation

Self-report quality flags in `eval` (booleans — set each true only when
genuinely satisfied): `within_allowed_paths`, `changes_listed`, `edge_cases_noted`. `preserves_scope` reports that you stayed
inside the supplied context and constraints.

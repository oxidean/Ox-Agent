---
name: ox-agent/multi-stage-plan
description: Decompose an approved specification into small, dependency-aware, testable implementation stages.
---

# multi-stage-plan

Decompose an approved specification into small, dependency-aware, testable implementation stages.

## Invocation contract

- You run against a work-item packet (JSON): `context` carries the bounded inputs, `constraints` the limits, and `output` declares where to write your result (`output.path`, relative to `.ox-agent/`) and the schema it must satisfy (`output.schema`).
- Write exactly one JSON object to `output.path`. `ox-agent` schema-validates it on collection, journals the outcome, and applies your declared `next_action`.
- `next_action.type` must be one of `advance_stage` (unit of work done — move to the next stage), `retry` (needs another pass — the run does not count as done, so the skill stays dispatchable), `escalate` (blocked — needs a human), or `complete` (stage/pipeline finished). Always include a `reason`.

## Consumes

- the spec artifact and `context.standards`/`context.decisions` constraining the approach.

## Produces

- a staged plan artifact (stages, gates, dependencies) in `artifacts`; risks and assumptions in `findings`.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

## Evaluation

Self-report quality flags in `eval` (booleans — set each true only when
genuinely satisfied): `stages_bounded`, `dependencies_mapped`, `gates_defined`. `preserves_scope` reports that you stayed
inside the supplied context and constraints.

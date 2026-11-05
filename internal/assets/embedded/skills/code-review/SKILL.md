---
name: ox-agent/code-review
description: Independently review an implementation against its specification, security boundaries, maintainability, and test quality.
---

# code-review

Independently review an implementation against its specification, security boundaries, maintainability, and test quality.

## Invocation contract

- You run against a work-item packet (JSON): `context` carries the bounded inputs, `constraints` the limits, and `output` declares where to write your result (`output.path`) and the schema it must satisfy (`output.schema`).
- Write exactly one JSON object to `output.path`. `ox-agent` schema-validates it on collection, journals the outcome, and applies your declared `next_action`.
- `next_action.type` must be one of `advance_stage` (unit of work done — move to the next stage), `retry` (needs another pass), `escalate` (blocked — needs a human), or `complete` (stage/pipeline finished). Always include a `reason`.

## Consumes

- the change scope in the packet (`context.changed_paths`, diff references) and the standards it must satisfy.

## Produces

- severity-ranked review comments in `findings` and a `verdict` that gates the verification stage; cite `file:line` for every finding.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

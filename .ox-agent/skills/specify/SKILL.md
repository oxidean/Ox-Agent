---
name: ox-agent/specify
description: Turn a work-item intent into a detailed, testable specification with explicit scope, contracts, and acceptance criteria.
---

# specify

Turn a work-item intent into a detailed, testable specification with explicit scope, contracts, and acceptance criteria.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

## Contract

- Consumes: the work-item packet from `ox-agent run` / `ox-agent context compile` (`context`, `constraints`, `prior_outputs`).
- Produces: a testable specification — explicit scope, contracts, and acceptance criteria — as `artifacts`.
- Output: write the result JSON to the packet's `output.path` per `schemas/output.schema.json`; declare `next_action.type` as `advance_stage`, `retry`, `escalate`, or `complete`.

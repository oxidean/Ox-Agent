---
name: ox-agent/tdd
description: Guide red-green-refactor implementation and produce evidence that behavior is covered by meaningful tests.
---

# tdd

Guide red-green-refactor implementation and produce evidence that behavior is covered by meaningful tests.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

## Contract

- Consumes: the work-item packet from `ox-agent run` / `ox-agent context compile` (`context`, `constraints`, `prior_outputs`).
- Produces: test-first implementation within `constraints.allowed_paths`: failing tests, then passing code, with coverage evidence in `findings`.
- Output: write the result JSON to the packet's `output.path` per `schemas/output.schema.json`; declare `next_action.type` as `advance_stage`, `retry`, `escalate`, or `complete`.

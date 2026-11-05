---
name: ox-agent/brainstorm
description: Run structured divergent and convergent analysis for a work item while preserving disagreement and producing decision-ready options.
---

# brainstorm

Run structured divergent and convergent analysis for a work item while preserving disagreement and producing decision-ready options.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

## Contract

- Consumes: the work-item packet from `ox-agent run` / `ox-agent context compile` (`context`, `constraints`, `prior_outputs`).
- Produces: decision-ready options with preserved trade-offs and disagreement in `findings`, ready for `specify` to consume.
- Output: write the result JSON to the packet's `output.path` per `schemas/output.schema.json`; declare `next_action.type` as `advance_stage`, `retry`, `escalate`, or `complete`.

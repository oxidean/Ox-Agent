---
name: ox-agent/adr
description: Identify and record consequential technical decisions as durable, supersedable architecture decision records.
---

# adr

Identify and record consequential technical decisions as durable, supersedable architecture decision records.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

## Contract

- Consumes: the work-item packet from `ox-agent run` / `ox-agent context compile` (`context`, `constraints`, `prior_outputs`).
- Produces: durable architecture decision records under `context/decisions/`, listed in `artifacts`; superseded ADRs are referenced, not deleted.
- Output: write the result JSON to the packet's `output.path` per `schemas/output.schema.json`; declare `next_action.type` as `advance_stage`, `retry`, `escalate`, or `complete`.

---
name: ox-agent/specify
description: Turn a work-item intent into a detailed, testable specification with explicit scope, contracts, and acceptance criteria.
---

# specify

Turn a work-item intent into a detailed, testable specification with explicit scope, contracts, and acceptance criteria.

## Invocation contract

- You run against a work-item packet (JSON): `context` carries the bounded inputs, `constraints` the limits, and `output` declares where to write your result (`output.path`, relative to `.ox-agent/`) and the schema it must satisfy (`output.schema`). `work_item_dir` and `artifacts_dir` give the resolved `.ox-agent`-relative paths of the work item's directory and its artifact directory.
- Write exactly one JSON object to `output.path`. `ox-agent` schema-validates it on collection, journals the outcome, and applies your declared `next_action`.
- `next_action.type` must be one of `advance_stage` (unit of work done — move to the next stage), `retry` (needs another pass — the run does not count as done, so the skill stays dispatchable), `escalate` (blocked on work — needs a human), `await_input` (needs a human answer — the run files a durable request and stays dispatchable), or `complete` (stage/pipeline finished). Always include a `reason`.
- On `escalate` the runtime files a durable input request and marks the item blocked — the human answers asynchronously via `ox-agent input respond`; do not wait for an in-turn reply.
- To ask the human a question without blocking the item, use `next_action.type: await_input` with the question in `reason` (add `options` for a decision request). The runtime files a durable request, the run stays dispatchable, and the answer lands in your next packet under `context.inputs`. Never rely on a synchronous host prompt — async-chat surfaces expire it before a human sees it.

## Consumes

- the chosen brainstorm direction, relevant standards/decisions, and `context.specs` for any spec of record on this subject.

## Produces

- a spec artifact (path in `artifacts`) with acceptance criteria a `verify` run can check — `covers:` globs on it, and `spec_impact` declaring whether it amends an existing spec of record.

## Spec reconciliation

- Check `context.specs` for an existing spec on this subject: your spec either amends it — declare that in `spec_impact` and edit the promoted file — or establishes a new spec of record.
- Write `covers:` front-matter globs on the spec naming the repo paths it governs (e.g. `internal/harness/**`) — coverage is what lets collection catch later changes that never consulted it.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Work-item artifacts go under `artifacts_dir` and the item's directory is `work_item_dir` — both come resolved in the packet, so never derive or glob the work-item path yourself. Content with a declared home elsewhere (context entries, decisions) keeps its own contract location. Promote durable decisions and blockers into the appropriate work-item artifact.

## Evaluation

Self-report quality flags in `eval` (booleans — set each true only when
genuinely satisfied): `acceptance_criteria_present`, `scope_explicit`, `contracts_defined`. `preserves_scope` reports that you stayed
inside the supplied context and constraints.

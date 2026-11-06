import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

// The invocation contract and operating rules are shared across every core
// skill; only the description and the Consumes/Produces sections differ.
// Keep these in sync with the maintained SKILL.md files — the generator must
// regenerate them byte-for-byte or it will silently drop the contracts.
const invocationContract = [
  '## Invocation contract',
  '',
  '- You run against a work-item packet (JSON): `context` carries the bounded inputs, `constraints` the limits, and `output` declares where to write your result (`output.path`, relative to `.ox-agent/`) and the schema it must satisfy (`output.schema`).',
  '- Write exactly one JSON object to `output.path`. `ox-agent` schema-validates it on collection, journals the outcome, and applies your declared `next_action`.',
  '- `next_action.type` must be one of `advance_stage` (unit of work done — move to the next stage), `retry` (needs another pass — the run does not count as done, so the skill stays dispatchable), `escalate` (blocked on work — needs a human), `await_input` (needs a human answer — the run files a durable request and stays dispatchable), or `complete` (stage/pipeline finished). Always include a `reason`.',
].join('\n');

const operatingRules = [
  '## Operating rules',
  '',
  '- Load only the current work-item packet and referenced artifacts.',
  '- Preserve evidence and disagreement rather than inventing certainty.',
  '- Do not exceed the permissions or paths in the input contract.',
  '- Return a compact structured result matching the output schema.',
  '- Promote durable decisions and blockers into the appropriate work-item artifact.',
].join('\n');

const skills = {
  'discover-context': {
    category: 'discovery',
    description: 'Discover relevant project standards, architecture, ADRs, code patterns, tests, and constraints for the current work item.',
    consumes: '- the repository around the project plus seeded `context/` files.',
    produces: '- context entries under `context/` (standards, decisions, references) and a `findings` summary; entries enrich every later packet via `context index`.',
  },
  brainstorm: {
    category: 'brainstorming',
    description: 'Run structured divergent and convergent analysis for a work item while preserving disagreement and producing decision-ready options.',
    consumes: '- the problem framing in the packet plus `context.references` for prior exploration.',
    produces: '- candidate directions as `artifacts` (one per option) with tradeoffs and open questions in `findings`; feeds `specify` once a direction is chosen.',
  },
  specify: {
    category: 'specification',
    description: 'Turn a work-item intent into a detailed, testable specification with explicit scope, contracts, and acceptance criteria.',
    consumes: '- the chosen brainstorm direction and relevant standards/decisions.',
    produces: '- a spec artifact (path in `artifacts`) with acceptance criteria a `verify` run can check.',
  },
  adr: {
    category: 'architecture',
    description: 'Identify and record consequential technical decisions as durable, supersedable architecture decision records.',
    consumes: '- the work-item packet — especially `context.decisions` (prior ADRs) and `context.standards`, so new records supersede rather than contradict.',
    produces: '- ADR files under `context/decisions/<slug>.md` — durable, supersedable records. List each written path in `artifacts`; record superseded ADRs in `findings`.',
  },
  'multi-stage-plan': {
    category: 'planning',
    description: 'Decompose an approved specification into small, dependency-aware, testable implementation stages.',
    consumes: '- the spec artifact and `context.standards`/`context.decisions` constraining the approach.',
    produces: '- a staged plan artifact (stages, gates, dependencies) in `artifacts`; risks and assumptions in `findings`.',
  },
  tdd: {
    category: 'testing',
    description: 'Guide red-green-refactor implementation and produce evidence that behavior is covered by meaningful tests.',
    consumes: '- `context.project.test_command` and `constraints.allowed_paths`.',
    produces: '- failing-test-first then implementation artifacts as `changed_paths`; test outcomes in `findings` (red-to-green evidence).',
  },
  implementation: {
    category: 'implementation',
    description: 'Implement one bounded, approved work-item stage while respecting scope, standards, and file constraints.',
    consumes: '- the spec/plan artifacts in `context.references` and `constraints.allowed_paths` bounding edits.',
    produces: '- code changes listed in `artifacts` as `changed_paths` entries with one-line summaries; failure edges in `findings`.',
  },
  'code-review': {
    category: 'review',
    description: 'Independently review an implementation against its specification, security boundaries, maintainability, and test quality.',
    consumes: "- the implementation run's output via `context.prior_outputs` — its `artifacts` list the changed paths to review — plus the standards the change must satisfy.",
    produces: '- severity-ranked review comments in `findings` and a `verdict` that gates the verification stage; cite `file:line` for every finding.',
  },
  verify: {
    category: 'verification',
    description: 'Verify tests, traceability, policy compliance, rollback readiness, and operational requirements before shipping.',
    consumes: "- `context.project.test_command`, the spec's acceptance criteria, and the implementation run's `changed_paths` artifacts via `context.prior_outputs`.",
    produces: '- verification results in `findings` (command, outcome, evidence) and a `pass`/`needs_rework` `verdict`.',
  },
  discussion: {
    category: 'coordination',
    description: 'Record asynchronous agent discussions, summarize relevant conclusions, and promote decisions without losing provenance.',
    consumes: '- the open thread under `discussions/` named by the packet context.',
    produces: '- a summarized position suitable for `ox-agent discuss add`/`summarize`; key points in `findings`, no state writes outside the thread.',
  },
  progress: {
    category: 'coordination',
    description: 'Record compact, structured progress updates and derive resumable work-item status from lifecycle events.',
    consumes: '- `state/events.ndjson` and work-item `index.json` files — read via `ox-agent events`/`status`/`state replay`, never invented.',
    produces: '- a progress summary (`summary` + `findings`) naming blockers, completed stages, and the next legal actions.',
  },
  shipping: {
    category: 'shipping',
    description: 'Prepare a verified work item for delivery with release notes, rollout evidence, and rollback instructions.',
    consumes: '- verification results, open gates from `policy.yaml`, and the item\'s artifact set.',
    produces: '- a ship-readiness `verdict` plus `findings` listing outstanding gates; `escalate` when a human approval is required.',
  },
  retrospective: {
    category: 'retrospective',
    description: 'Analyze a completed work item for quality, cost, rework, and process improvements without silently changing policy.',
    consumes: '- the completed item\'s journal and produced artifacts.',
    produces: '- a retro summary in `summary`/`findings`; durable learnings promoted into `context/standards/` or `context/decisions/` so later packets inherit them.',
  },
};

const root = path.resolve('skills');
for (const [name, skill] of Object.entries(skills)) {
  const { category, description, consumes, produces } = skill;
  const dir = path.join(root, name);
  await mkdir(path.join(dir, 'schemas'), { recursive: true });
  await mkdir(path.join(dir, 'evals'), { recursive: true });
  const qualified = `ox-agent/${name}`;
  const safeDescription = description.replace(/\n/g, ' ');
  const yamlDescription = safeDescription.match(/.{1,72}(?:\s|$)/g)?.map((line) => `  ${line.trim()}`).join('\n') ?? `  ${safeDescription}`;
  const evalPrompt = safeDescription.match(/.{1,72}(?:\s|$)/g)?.map((line) => `      ${line.trim()}`).join('\n') ?? `      ${safeDescription}`;
  await writeFile(path.join(dir, 'skill.yaml'), `schema_version: 1\nname: ${qualified}\nversion: 0.1.0\nkind: core\ncategory: ${category}\ndescription: >-\n${yamlDescription}\ncompatibility:\n  skill_api: 1\n  ox_agent: '>=0.1.0-0 <0.2.0'\ninputs:\n  schema: schemas/input.schema.json\noutputs:\n  schema: schemas/output.schema.json\nevaluation:\n  suite: evals/evals.yaml\n  minimum_pass_rate: 0.9\n`);
  await writeFile(path.join(dir, 'SKILL.md'), `---\nname: ${qualified}\ndescription: ${safeDescription}\n---\n\n# ${name}\n\n${description}\n\n${invocationContract}\n\n## Consumes\n\n${consumes}\n\n## Produces\n\n${produces}\n\n${operatingRules}\n`);
  await writeFile(path.join(dir, 'schemas', 'input.schema.json'), JSON.stringify({ '$schema': 'http://json-schema.org/draft-07/schema#', type: 'object', required: ['schema_version', 'skill', 'work_item_id', 'context', 'constraints'], properties: { schema_version: { const: 1 }, skill: { const: qualified }, work_item_id: { type: 'string' }, context: { type: 'object' }, constraints: { type: 'object' } } }, null, 2) + '\n');
  await writeFile(path.join(dir, 'schemas', 'output.schema.json'), JSON.stringify({ '$schema': 'http://json-schema.org/draft-07/schema#', type: 'object', required: ['schema_version', 'invocation_id', 'skill', 'status', 'verdict', 'summary', 'artifacts', 'findings', 'next_action'], properties: { schema_version: { const: 1 }, invocation_id: { type: 'string' }, skill: { const: qualified }, status: { enum: ['complete', 'blocked', 'failed'] }, verdict: { enum: ['pass', 'pass_with_notes', 'needs_rework', 'blocked'] }, summary: { type: 'string' }, artifacts: { type: 'array' }, findings: { type: 'array' }, next_action: { type: 'object', required: ['type', 'reason'], properties: { type: { enum: ['advance_stage', 'retry', 'escalate', 'await_input', 'complete'] }, reason: { type: 'string' }, options: { type: 'array', items: { type: 'string', minLength: 1 }, minItems: 2, description: 'answer choices for an await_input decision request' } }, additionalProperties: true } } }, null, 2) + '\n');
  await writeFile(path.join(dir, 'evals', 'evals.yaml'), `schema_version: 1\nskill: ${qualified}\ncases:\n  - id: valid-scoped-request\n    prompt: >-\n${evalPrompt}\n    expected:\n      output_schema_valid: true\n  - id: respects-scope\n    prompt: >-\n      Execute the skill using only the supplied work-item context and\n      constraints.\n    expected:\n      output_schema_valid: true\n      preserves_scope: true\nthresholds:\n  schema_pass_rate: 1.0\n  quality_pass_rate: 0.9\n`);
}

import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

const skills = {
  'discover-context': ['discovery', 'Discover relevant project standards, architecture, ADRs, code patterns, tests, and constraints for the current work item.'],
  brainstorm: ['brainstorming', 'Run structured divergent and convergent analysis for a work item while preserving disagreement and producing decision-ready options.'],
  specify: ['specification', 'Turn a work-item intent into a detailed, testable specification with explicit scope, contracts, and acceptance criteria.'],
  adr: ['architecture', 'Identify and record consequential technical decisions as durable, supersedable architecture decision records.'],
  'multi-stage-plan': ['planning', 'Decompose an approved specification into small, dependency-aware, testable implementation stages.'],
  tdd: ['testing', 'Guide red-green-refactor implementation and produce evidence that behavior is covered by meaningful tests.'],
  implementation: ['implementation', 'Implement one bounded, approved work-item stage while respecting scope, standards, and file constraints.'],
  'code-review': ['review', 'Independently review an implementation against its specification, security boundaries, maintainability, and test quality.'],
  verify: ['verification', 'Verify tests, traceability, policy compliance, rollback readiness, and operational requirements before shipping.'],
  discussion: ['coordination', 'Record asynchronous agent discussions, summarize relevant conclusions, and promote decisions without losing provenance.'],
  progress: ['coordination', 'Record compact, structured progress updates and derive resumable work-item status from lifecycle events.'],
  shipping: ['shipping', 'Prepare a verified work item for delivery with release notes, rollout evidence, and rollback instructions.'],
  retrospective: ['retrospective', 'Analyze a completed work item for quality, cost, rework, and process improvements without silently changing policy.'],
};

const root = path.resolve('skills');
for (const [name, [category, description]] of Object.entries(skills)) {
  const dir = path.join(root, name);
  await mkdir(path.join(dir, 'schemas'), { recursive: true });
  await mkdir(path.join(dir, 'evals'), { recursive: true });
  const qualified = `ox-agent/${name}`;
  const safeDescription = description.replace(/\n/g, ' ');
  const yamlDescription = safeDescription.match(/.{1,72}(?:\s|$)/g)?.map((line) => `  ${line.trim()}`).join('\n') ?? `  ${safeDescription}`;
  const evalPrompt = safeDescription.match(/.{1,72}(?:\s|$)/g)?.map((line) => `      ${line.trim()}`).join('\n') ?? `      ${safeDescription}`;
  await writeFile(path.join(dir, 'skill.yaml'), `schema_version: 1\nname: ${qualified}\nversion: 0.1.0\nkind: core\ncategory: ${category}\ndescription: >-\n${yamlDescription}\ncompatibility:\n  skill_api: 1\n  ox_agent: '>=0.1.0 <0.2.0'\ninputs:\n  schema: schemas/input.schema.json\noutputs:\n  schema: schemas/output.schema.json\nevaluation:\n  suite: evals/evals.yaml\n  minimum_pass_rate: 0.9\n`);
  await writeFile(path.join(dir, 'SKILL.md'), `---\nname: ${qualified}\ndescription: ${safeDescription}\n---\n\n# ${name}\n\n${description}\n\n## Operating rules\n\n- Load only the current work-item packet and referenced artifacts.\n- Preserve evidence and disagreement rather than inventing certainty.\n- Do not exceed the permissions or paths in the input contract.\n- Return a compact structured result matching the output schema.\n- Promote durable decisions and blockers into the appropriate work-item artifact.\n`);
  await writeFile(path.join(dir, 'schemas', 'input.schema.json'), JSON.stringify({ '$schema': 'http://json-schema.org/draft-07/schema#', type: 'object', required: ['schema_version', 'skill', 'work_item_id', 'context', 'constraints'], properties: { schema_version: { const: 1 }, skill: { const: qualified }, work_item_id: { type: 'string' }, context: { type: 'object' }, constraints: { type: 'object' } } }, null, 2) + '\n');
  await writeFile(path.join(dir, 'schemas', 'output.schema.json'), JSON.stringify({ '$schema': 'http://json-schema.org/draft-07/schema#', type: 'object', required: ['schema_version', 'invocation_id', 'skill', 'status', 'verdict', 'summary', 'artifacts', 'findings', 'next_action'], properties: { schema_version: { const: 1 }, invocation_id: { type: 'string' }, skill: { const: qualified }, status: { enum: ['complete', 'blocked', 'failed'] }, verdict: { enum: ['pass', 'pass_with_notes', 'needs_rework', 'blocked'] }, summary: { type: 'string' }, artifacts: { type: 'array' }, findings: { type: 'array' }, next_action: { type: 'object' } } }, null, 2) + '\n');
  await writeFile(path.join(dir, 'evals', 'evals.yaml'), `schema_version: 1\nskill: ${qualified}\ncases:\n  - id: valid-scoped-request\n    prompt: >-\n${evalPrompt}\n    expected:\n      output_schema_valid: true\n  - id: respects-scope\n    prompt: >-\n      Execute the skill using only the supplied work-item context and\n      constraints.\n    expected:\n      output_schema_valid: true\n      preserves_scope: true\nthresholds:\n  schema_pass_rate: 1.0\n  quality_pass_rate: 0.9\n`);
}

# Contributing to Ox-Agent

Ox-Agent is a greenfield TypeScript CLI and skill distribution. Keep the core deterministic and token-efficient.

## Rules

- Keep all target-project artifacts under `.ox-agent/`.
- Do not add project-local skill overrides; modify skills in this repository or an explicit fork.
- Validate changes with `npm run build`, `npm test`, and `npm run validate`.
- Keep CLI commands deterministic. LLM calls belong behind skill or harness contracts.
- Preserve append-only event and discussion semantics.
- New skills require `skill.yaml`, `SKILL.md`, input/output schemas, and evaluation cases.

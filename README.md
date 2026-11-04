# Ox-Agent

Ox-Agent is an installable, repository-local AI-SDLC kit. It keeps durable work artifacts under `.ox-agent/`, uses small task-specific context packets, and supports official or forked skills through the `npx skills` ecosystem.

## Status

This repository contains the Phase 0 scaffold: a Node.js CLI, schemas, skill contracts, repository initialization, deterministic validation, work-item tracking, and skill installation/update plumbing. LLM orchestration, Hermes dispatch, and automated summarisation are deliberately next-stage features.

## Try it in a test project

The easiest way to try Ox-Agent without changing an existing application is to create a temporary project:

```bash
mkdir ox-agent-sandbox
cd ox-agent-sandbox
npm init -y
npm install --save-dev /path/to/ox-agent
npx ox-agent init
npx ox-agent doctor
npx ox-agent start "Add a health-check endpoint"
npx ox-agent status
```

For local development, run the CLI directly from this repository:

```bash
npm install
npm run build
node dist/cli.js init
node dist/cli.js doctor
node dist/cli.js start "Describe a change"
node dist/cli.js status
```

The current scaffold installs the initial `brainstorm` skill. Additional official skills will be added as their metadata, schemas, references, and evaluation suites are implemented.

`init` is deliberately non-destructive: it refuses to overwrite an existing `.ox-agent/` directory.

To try the skill integration from a local Ox-Agent checkout:

```bash
npx ox-agent skills install /path/to/ox-agent --skill brainstorm
npx ox-agent skills list
npx ox-agent skills check
```

For a published or forked source, replace `/path/to/ox-agent` with a GitHub repository or Git URL. Skill links are created through `npx skills` and mirrored into `.ox-agent/skills/`; the selected source is recorded in `.ox-agent/skills.lock`.

## Current Phase 0 scope

Implemented today:

- TypeScript/Node.js CLI with `init`, `doctor`, `start`, and `status`.
- Repository-local `.ox-agent/` configuration and work-item layout.
- JSON/YAML contract validation through `doctor`.
- Append-only lifecycle events in `.ox-agent/state/events.ndjson`.
- Initial skill manifest and evaluation contract.
- `npx skills` integration commands: `skills list`, `skills install`, `skills check`, and `skills update`.

Not yet implemented:

- Hermes dispatch and async task persistence.
- Automated discussion summarisation.
- Context-packet compilation.
- Full lifecycle stage execution.
- LLM-backed evaluation runs.
- Complete official skill set beyond the initial scaffold.

## CLI reference

```text
ox-agent init                         Create .ox-agent/
ox-agent doctor                       Validate local configuration and artifacts
ox-agent start <title>                Create a work item
ox-agent status [WI-0001]             Show active and archived work
ox-agent skills list                  List installed Ox-Agent skills
ox-agent skills install [source]      Install skills through npx skills
ox-agent skills check                 Check for available skill updates
ox-agent skills update [--yes]        Update skills with confirmation
```

`skills install` uses the configured source from `.ox-agent/config.yaml`, or `oxidean/ox-agent` by default. It installs harness-visible links through `npx skills`; the source/ref is recorded in `.ox-agent/skills.lock`.

## Continuous integration

The repository includes `.github/workflows/ci.yml`. Pull requests targeting `main` and pushes to `main` or `init` run:

1. `npm ci`
2. TypeScript compilation with `npm run build`
3. The test suite with `npm test`
4. Repository validation with `npm run validate`
5. A CLI smoke test in a temporary project that runs `init`, `doctor`, `start`, and `status`

This ensures both the Ox-Agent source and the generated `.ox-agent/` setup remain valid. The workflow intentionally does not install remote skills or invoke LLMs, so CI remains deterministic and does not depend on external agent credentials.

## Repository-local layout

All installed project state lives under `.ox-agent/`:

- `config.yaml` and `policy.yaml` configure the project and autonomy boundaries.
- `skills/` is the harness-visible Ox-Agent skill surface, linked from the selected source.
- `work/active/` contains work-item artifacts and compact progress records.
- `state/events.ndjson` is the append-only lifecycle event log.
- `runtime/` contains generated, non-canonical execution data.

Skills are official Ox-Agent behavior. Projects configure them but do not patch them in place; customization is distributed through a pinned Ox-Agent fork. Every skill must provide metadata, schemas, and evaluations.

## Principles

- Index-first, minimal context loading.
- Discussions are durable, append-only, and summarized before reuse.
- Profiles are managed by policy; agents may execute a profile but may not downgrade it.
- The CLI enforces deterministic contracts; reasoning belongs in skills and harnesses.

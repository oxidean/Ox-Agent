# Ox-Agent

**Repository-local, token-efficient AI-SDLC for coding agents.**

Ox-Agent adds a durable, structured development workflow to an existing
repository. It keeps work-item state, lifecycle records, decisions, and agent
skills close to the codebase under `.ox-agent/`.

> Ox-Agent is currently in Phase 0. The repository-local CLI and artifact
> contracts are available today. Full agent orchestration, Hermes integration,
> automated discussion summaries, and end-to-end lifecycle execution are being
> built incrementally.

## Why Ox-Agent?

Coding agents are powerful, but their context is often temporary, expensive,
and difficult to audit. Ox-Agent provides:

- **Repository-local memory** — durable state under `.ox-agent/`.
- **Token-efficient context** — task-specific context instead of loading every
  lifecycle artifact into every agent session.
- **Structured work items** — explicit state, progress, and lifecycle records.
- **Governed skills** — versioned capabilities installed through `npx skills`.
- **Fork-based customization** — change methodology through a pinned Ox-Agent
  fork rather than local prompt patches.

## Prerequisites

- Bun `>= 1.2`
- Git
- Network access for remote skill installation
- A compatible coding-agent harness for future skill execution

Ox-Agent is not published to npm yet. The quickstart below installs it from a
local checkout.

## Quickstart

```bash
git clone https://github.com/oxidean/Ox-Agent.git
cd Ox-Agent
bun install
bun run build

mkdir ../ox-agent-test-project
cd ../ox-agent-test-project
bun init -y
bun add --dev file:../Ox-Agent

bun node_modules/ox-agent/dist/cli.js init
bun node_modules/ox-agent/dist/cli.js doctor
bun node_modules/ox-agent/dist/cli.js start "Add a health-check endpoint"
bun node_modules/ox-agent/dist/cli.js status
```

After Ox-Agent is published, the same commands can use `bunx ox-agent`.
For a local checkout, invoke the installed binary directly as shown above.
`start` currently creates work-item scaffolding; it does not yet dispatch an
agent or execute the requested change.

## Current status

Available today:

- TypeScript/Node.js CLI with `init`, `doctor`, `start`, and `status`.
- Repository-local `.ox-agent/` configuration and work-item layout.
- Supported JSON/YAML contract validation through `doctor`.
- Append-only lifecycle events in `.ox-agent/state/events.ndjson`.
- Initial `brainstorm` skill manifest, schemas, and evaluation contract.
- `npx skills` integration commands: `skills list`, `skills install`,
  `skills check`, and `skills update`.

Not yet implemented:

- Hermes dispatch and async task persistence.
- Automated discussion summarisation.
- Context-packet compilation.
- Full lifecycle stage execution.
- LLM-backed evaluation runs.
- The remaining official skills.

## CLI reference

```text
bunx ox-agent init                         Create .ox-agent/
bunx ox-agent doctor                       Validate supported contracts
bunx ox-agent start <title>                Create a work item
bunx ox-agent status [WI-0001]             Show active and archived work
bunx ox-agent skills list                  List installed Ox-Agent skills
bunx ox-agent skills install [source]      Install skills through npx skills
bunx ox-agent skills check                 Check for available skill updates
bunx ox-agent skills update [--yes]        Update skills with confirmation
```

See [`docs/cli.md`](docs/cli.md) for command behavior and options.

## Continuous integration

The repository includes `.github/workflows/ci.yml`. Pull requests targeting
`main` and pushes to `main` or `init` run `bun install --frozen-lockfile`,
`bun run build`, `bun test`, `bun run validate`, and a CLI smoke test in a
temporary project. CI does not install remote skills or invoke LLMs, so these
checks remain deterministic.

## Configuration and governance

`config.yaml` selects the project, workflow profile, and enabled skills.
`policy.yaml` controls autonomy boundaries, approvals, and agent budgets.
Profiles are managed by project policy: agents may execute a selected profile
but may not silently downgrade it.

`bunx ox-agent init` creates one repository-local directory:

| Path | Purpose |
|---|---|
| `config.yaml` | Project, workflow, and enabled-skill configuration |
| `policy.yaml` | Autonomy, approval, and agent budget policy |
| `context/` | Project context and standards |
| `lifecycle/` | Lifecycle state definitions |
| `skills/` | Installed Ox-Agent skill surface |
| `work/active/` | Current work items |
| `work/archived/` | Archived work items |
| `state/events.ndjson` | Append-only lifecycle events |
| `runtime/` | Temporary generated execution data |
| `integrations/` | Harness integration configuration |

A new work item currently contains:

```text
.ox-agent/work/active/WI-0001-example/
├── index.yaml
├── status.yaml
└── progress.ndjson
```

Future lifecycle stages will add specifications, ADRs, plans, discussions,
reviews, and shipping records to this directory.

## Basic workflow

The intended Ox-Agent workflow is:

```text
Start a work item
  → Discover context
  → Brainstorm options
  → Write specifications and ADRs
  → Create an implementation plan
  → Implement with TDD
  → Review and verify
  → Ship and observe
```

Current capability status:

| Capability | Status |
|---|---|
| Initialize `.ox-agent/` | Available |
| Validate supported contracts | Available |
| Create and list work items | Available |
| Brainstorm skill contract | Initial scaffold |
| Specification workflow | Planned |
| ADR workflow | Planned |
| Multi-stage planning | Planned |
| TDD implementation | Planned |
| Code review orchestration | Planned |
| Hermes async dispatch | Planned |
| Discussion summarisation | Planned |
| Autonomous shipping | Planned |

## Skills

Skills are versioned Ox-Agent capabilities used by agents during the
lifecycle. The initial available skill is:

- `brainstorm` — structured divergent and convergent analysis.

The following skills are planned:

- `specify`
- `adr`
- `multi-stage-plan`
- `tdd`
- `code-review`
- `shipping`
- `discussion`
- `progress`

Install the current local skill with:

```bash
bunx ox-agent skills install /path/to/ox-agent --skill brainstorm
bunx ox-agent skills list
bunx ox-agent skills check
bunx ox-agent skills update
```

The CLI delegates installation and updates to the external `npx skills` tool,
which remains intentionally separate from Bun. Official skills are managed by
Ox-Agent. Projects configure them but do not patch them locally; organization-
specific behavior belongs in a pinned Ox-Agent fork. See
[`skills/README.md`](skills/README.md) for the skill contract.

`skills install` uses the configured source from `.ox-agent/config.yaml`, or
`oxidean/ox-agent` by default. The selected source is recorded in
`.ox-agent/skills.lock`.

## Roadmap

The next milestones are complete official skills, discussion records with
automated summaries, context-packet compilation, lifecycle transitions and
gates, local and Hermes async dispatch, TDD/review/shipping orchestration, and
skill evaluation plus token-efficiency metrics.

## More documentation

- [`docs/cli.md`](docs/cli.md) — command behavior and options.
- [`skills/README.md`](skills/README.md) — skill package contract.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — development and contribution setup.

## Principles

- Index-first, minimal context loading.
- Discussions are durable, append-only, and summarized before reuse.
- Profiles are managed by policy; agents may execute a profile but may not downgrade it.
- The CLI enforces deterministic contracts; reasoning belongs in skills and harnesses.

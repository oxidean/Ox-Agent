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
- **Governed skills** — versioned capabilities installed through `bunx skills`.
- **Fork-based customization** — change methodology through a pinned Ox-Agent
  fork rather than local prompt patches.

## Prerequisites

- Bun `>= 1.4`
- GitHub Packages access with a token that has `read:packages`
- A compatible coding-agent harness for future skill execution

## Quickstart

Ox-Agent is distributed as the scoped GitHub Packages package
`@oxidean/ox-agent`. Configure the `@oxidean` scope in your user-level
`.npmrc` (Bun uses this registry configuration too):

```ini
@oxidean:registry=https://npm.pkg.github.com
//npm.pkg.github.com/:_authToken=${GITHUB_TOKEN}
```

Set `GITHUB_TOKEN` to a GitHub token with access to the package, then run these
commands from the repository you want to manage:

```bash
bunx --bun --package @oxidean/ox-agent ox-agent init
bunx --bun --package @oxidean/ox-agent ox-agent doctor
bunx --bun --package @oxidean/ox-agent ox-agent start \
  "Add a health-check endpoint"
bunx --bun --package @oxidean/ox-agent ox-agent status
```

For repeated use, install the CLI globally:

```bash
bun add --global @oxidean/ox-agent --registry https://npm.pkg.github.com
ox-agent init
```

`init` is non-destructive and refuses to overwrite an existing `.ox-agent/`
directory. `start` currently creates work-item scaffolding; it does not yet
dispatch an agent or execute the requested change.

> The GitHub Packages release workflow must publish a version before the
> package commands above can be used. Contributors working from a checkout
> should follow [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Current status

Available today:

- TypeScript/Node.js CLI with `init`, `doctor`, `start`, and `status`.
- Repository-local `.ox-agent/` configuration and work-item layout.
- Supported JSON/YAML contract validation through `doctor`.
- Append-only lifecycle events in `.ox-agent/state/events.ndjson`.
- Initial `brainstorm` skill manifest, schemas, and evaluation contract.
- All official lifecycle skills are packaged and enabled by default; the
  orchestrator will load them by lifecycle stage.
- `bunx skills` integration commands: `skills list`, `skills install`,
  `skills check`, and `skills update`.

Not yet implemented:

- Hermes dispatch and async task persistence.
- Automated discussion summarisation.
- Context-packet compilation.
- Full lifecycle stage execution.
- LLM-backed evaluation runs.
- Agent implementation, review, and shipping orchestration.

## CLI reference

```text
bunx ox-agent init                         Create .ox-agent/
bunx ox-agent doctor                       Validate supported contracts
bunx ox-agent start <title>                Create a work item
bunx ox-agent status [WI-0001]             Show active and archived work
bunx ox-agent skills list                  List installed Ox-Agent skills
bunx ox-agent skills install [source]      Install skills through bunx skills
bunx ox-agent skills check                 Check for available skill updates
bunx ox-agent skills update [--yes]        Update skills with confirmation
```

See [`docs/cli.md`](docs/cli.md) for command behavior and options.

## Continuous integration

The repository includes `.github/workflows/ci.yml`. Pull requests targeting
`main` and pushes to `main` run `bun install --frozen-lockfile`, `bun run build`,
`bun test`, `bun run validate`, package-content validation, and a CLI smoke test
in a temporary project. CI does not install remote skills or invoke LLMs, so
these checks remain deterministic.

Tagged releases use `.github/workflows/release.yml` to validate, package, and
publish `@oxidean/ox-agent` to GitHub Packages.

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

Skills are versioned Ox-Agent capabilities used by agents during the lifecycle.
`.ox-agent/skills/` contains the complete official lifecycle skill set:

- `discover-context`
- `brainstorm`
- `specify`
- `adr`
- `multi-stage-plan`
- `tdd`
- `implementation`
- `code-review`
- `verify`
- `discussion`
- `progress`
- `shipping`
- `retrospective`

The current Phase 0 CLI packages and validates these skills. Full agent
execution of each lifecycle stage is implemented incrementally, but every skill
uses the same metadata, schema, and evaluation contract.

Install a selected skill with:

```bash
bunx ox-agent skills install /path/to/ox-agent --skill brainstorm
bunx ox-agent skills list
bunx ox-agent skills check
bunx ox-agent skills update
```

The CLI delegates installation and updates to the external `bunx skills` tool.
Official skills are managed by Ox-Agent. Projects configure them but do not
patch them locally; organization-specific behavior belongs in a pinned Ox-Agent
fork. See [`skills/README.md`](skills/README.md) for the skill contract.

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

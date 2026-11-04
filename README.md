# Ox-Agent

**Repository-local, token-efficient AI-SDLC for coding agents.**

Ox-Agent is a globally installed Go CLI that executes the skills, workflows, and
pipelines defined by each repository's `.ox-agent/` directory. Project state,
standards, work items, and decisions remain local and version-controlled.

> The standalone Go distribution is the active migration target. The current
> binary foundation provides repository initialization, validation, work-item
> creation, status, embedded skills, and release-based installation. Full agent
> orchestration is being built incrementally.

## Why Ox-Agent?

- **Repository-local memory** — durable state under `.ox-agent/`.
- **Token-efficient context** — workflows can select only the context needed for
  the current stage.
- **Structured work items** — explicit state, progress, and lifecycle records.
- **Portable distribution** — one globally installed binary, with no npm, Bun,
  or project-local runtime dependency.
- **Configurable execution** — repository-local skills, workflows, and pipelines
  define what the binary executes.
- **Fork-friendly** — organizations can distribute a compatible Ox-Agent asset
  set through their own fork.

## Install

Install the latest release globally:

```bash
curl -fsSL \
  https://github.com/oxidean/Ox-Agent/releases/latest/download/install.sh \
  | bash
```

The installer detects the platform, downloads the matching release archive,
verifies its SHA-256 checksum, and installs `ox-agent` to `~/.local/bin`.
Ensure that directory is on your `PATH`.

Install a specific version:

```bash
OX_AGENT_VERSION=v0.2.0 \
  curl -fsSL \
  https://github.com/oxidean/Ox-Agent/releases/download/v0.2.0/install.sh \
  | bash
```

Verify the installation:

```bash
ox-agent version
```

Supported release targets currently include macOS and Linux on amd64 and arm64.
Windows installation uses `install.ps1` from the matching GitHub release.

## Quickstart

From the repository you want to manage:

```bash
ox-agent init
ox-agent doctor
ox-agent start "Add a health-check endpoint"
ox-agent status
```

`init` is non-destructive and refuses to overwrite an existing `.ox-agent/`
directory. The globally installed binary materializes the official skills,
workflows, pipelines, and schemas into `.ox-agent/`.

`start` currently creates work-item scaffolding. It does not yet dispatch agents
or execute the requested change.

## Repository-local layout

```text
.ox-agent/
├── framework.yaml       # binary and schema compatibility
├── config.yaml          # project configuration
├── policy.yaml          # autonomy and approval policy
├── skills/              # repository-local skill definitions
├── workflows/           # lifecycle stage and gate definitions
├── pipelines/           # ordered execution plans
├── lifecycle/           # state definitions
├── context/             # project standards and context
├── work/                # active and archived work items
├── state/               # append-only event journal and snapshots
├── integrations/        # harness configuration
└── runtime/             # generated packets, runs, cache, and locks
```

The stable separation is:

```text
binary   = how execution works
skill    = what a role does
workflow = when capabilities apply
pipeline = how work is ordered
work     = what happened
```

## Current CLI

```text
ox-agent version                    Show the installed binary version
ox-agent init                       Initialize .ox-agent/
ox-agent doctor                     Validate the local Ox-Agent setup
ox-agent start <title>              Create a work item
ox-agent status                     List active work items
```

The global binary is the only required runtime. It should not be installed in
`node_modules`, `.ox-agent`, or a project-specific vendor directory.

## Current status

Available in the standalone foundation:

- Global Go binary with `version`, `init`, `doctor`, `start`, and `status`.
- Embedded official skills, schemas, workflows, and pipelines.
- Repository-local `.ox-agent/` materialization.
- Cross-platform release packaging and checksum-based installation.

Next:

- `update`, install/uninstall controls, and signed release metadata.
- State replay, legal transitions, profiles, and gates.
- Context-packet compilation and token budgets.
- Durable discussions and automated summaries.
- Structured skill execution and local/Hermes harness adapters.
- TDD, review, verification, shipping, and retrospective pipelines.

## Skills and workflows

Official skills are materialized under `.ox-agent/skills/` and include:

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

Workflows select skills by lifecycle stage. Pipelines define ordered and
parallel agent execution. These files are project-local and inspectable; the
binary resolves and validates them without requiring a source checkout.

## Updates

The planned update flow is:

```bash
ox-agent update
ox-agent update --check
ox-agent update --version v0.2.0
```

Updates will verify release checksums or signatures and replace the global
binary atomically while preserving every repository's `.ox-agent/` state.

## Documentation

- [`docs/cli.md`](docs/cli.md) — command behavior.
- [`docs/install.md`](docs/install.md) — installation and platform details.
- [`docs/releasing.md`](docs/releasing.md) — binary release process.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — Go development and release setup.
- [`skills/README.md`](skills/README.md) — skill package contract.

## Principles

- Index-first, minimal context loading.
- Discussions are durable, append-only, and summarized before reuse.
- Profiles are managed by policy; agents may not silently downgrade them.
- The global binary enforces deterministic contracts; reasoning belongs in
  skills and harnesses.

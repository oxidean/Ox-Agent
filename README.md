# Ox-Agent

[![CI](https://github.com/oxidean/Ox-Agent/actions/workflows/ci.yml/badge.svg)](https://github.com/oxidean/Ox-Agent/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/oxidean/Ox-Agent)](https://github.com/oxidean/Ox-Agent/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/oxidean/Ox-Agent)](https://goreportcard.com/report/github.com/oxidean/Ox-Agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Repository-local, token-efficient AI-SDLC for coding agents.**

Ox-Agent is a globally installed Go CLI that executes the skills, workflows, and
pipelines defined by each repository's `.ox-agent/` directory. Project state,
standards, work items, and decisions remain local and version-controlled.

> The standalone Go distribution is the active line. The binary provides
> initialization, validation, work-item lifecycle, state replay, context-packet
> compilation, harness execution, pipelines, discussions, self-update, and
> release-based installation.

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
curl -fsSL \
  https://github.com/oxidean/Ox-Agent/releases/download/v0.1.3/install.sh \
  | OX_AGENT_VERSION=v0.1.3 bash
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

`run` dispatches the stage-mapped skill through a configured harness
integration — the manual adapter works with any agent. `run collect` validates
the recorded output and applies its next action.

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
ox-agent version                        Show the installed binary version
ox-agent init [--repair|--integrate]    Initialize or repair .ox-agent/
ox-agent integrate                      Wire AGENTS.md + .agents/.claude skill links
ox-agent doctor [--fix]                 Validate the local Ox-Agent setup
ox-agent skills list|check|update|install|uninstall
                                        Inspect and maintain the skill set
ox-agent start <title>                  Create a work item
ox-agent status [work-item]             List or inspect work items
ox-agent transition|advance|archive|unblock
                                        Lifecycle control
ox-agent input request|list|respond|wait|cancel
                                        Durable human-input requests
ox-agent run <wi> [--pipeline p]        Dispatch stage skills or a pipeline
ox-agent run collect <run-id>           Validate and apply a run's output
ox-agent review <wi> / ship <wi>        Pipeline shortcuts
ox-agent discuss open|add|summarize|close
                                        Durable discussion threads
ox-agent events [work-item]             Journal viewer
ox-agent state replay|explain           Fold or narrate the event journal
ox-agent context index|compile          Context index and packets
ox-agent eval <skill> [--emit]          Score skill eval cases
ox-agent update [--check|--version v]   Self-update binary (+ project assets)
ox-agent uninstall --yes                Remove the binary
```

See [`docs/cli.md`](docs/cli.md) for the full reference.

The global binary is the only required runtime. It should not be installed in
`node_modules`, `.ox-agent`, or a project-specific vendor directory.

## Current status

Available in the standalone distribution:

- Work-item lifecycle with legal transitions, gates, blocking, and archiving.
- Append-only event journal with replay, explain, and snapshots.
- Schema-validated doctor with `--fix` repair.
- Context indexing and token-budgeted packet compilation.
- Harness dispatch/collect through the manual adapter, with the contract
  enforced on both sides.
- Pipeline DAG execution honoring `policy.budgets.max_parallel_agents`.
- Durable discussion threads with compacted summaries.
- Durable human-input requests for async harness hosts — `await_input`
  leaves runs pending instead of blocking the turn.
- Harness dispatch through `manual`, `spawn-cli`, and `webhook` adapters.
- Remote skill installs pinned to a resolved commit SHA with provenance
  tracked in `remote-skills.lock`.
- Snapshot-accelerated journal replay.
- Skill evaluations scored against output schemas and quality assertions.
- Checksum-verified self-update and uninstall, with `.ox-agent` asset sync.

Next:

- Protocol-backed harness adapters, added per concrete consumer.
- Automated discussion summarization.
- Live harness-backed eval generation.

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

```bash
ox-agent update --check        # report installed vs latest
ox-agent update                # verify checksum and replace the binary
ox-agent update --version v0.1.3
ox-agent uninstall --yes
```

Updates verify release checksums and replace the global binary atomically
while preserving every repository's `.ox-agent/` state.

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — system model and contracts.
- [`docs/getting-started.md`](docs/getting-started.md) — first work-item walkthrough.
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

## Security

See [SECURITY.md](SECURITY.md) — report vulnerabilities through GitHub
Private Vulnerability Reporting. Release assets are checksum-verified and
signed with cosign (keyless, GitHub OIDC).

## License

[MIT](LICENSE) — Copyright (c) 2026 Oxidean.

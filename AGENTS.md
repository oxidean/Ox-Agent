# Contributing to Ox-Agent

Ox-Agent is a Go CLI and repository-local AI-SDLC distribution. Keep the binary
small, deterministic, and token-efficient.

## Rules

- The globally installed `ox-agent` binary is the execution boundary.
- Target repositories store project assets and state under `.ox-agent/`.
- Do not reintroduce npm, Bun, or project-local runtime installation.
- Preserve `.ox-agent/` compatibility and append-only event semantics.
- Official assets must be embedded and materialized consistently.
- New skills require metadata, instructions, input/output schemas, and evals.
- Run `go test ./...`, `go vet ./...`, and `go build ./cmd/ox-agent`.
- Checksum and release changes must be tested against a temporary project.

<!-- ox-agent:begin -->
## Ox-Agent toolkit

This repository is managed by `ox-agent` — a repository-local AI-SDLC
runtime. Project state, skills, and contracts live under `.ox-agent/`.

- `ox-agent doctor` validates the setup; `ox-agent start <title>` opens work items.
- Work items carry state in `index.json`; drive them with
  `ox-agent transition|archive|unblock|status`.
- Skills are contracts, not free-form prompts: `.ox-agent/skills/<name>/SKILL.md`
  plus input/output `schemas/`.
- Compile the bounded packet (`ox-agent context compile <wi> --skill <s>`)
  or dispatch via `ox-agent run <wi>`; results land in `runtime/runs/`
  and are schema-validated on collection.
- Materialized skills also resolve under `.agents/skills/` for harnesses
  that scan that path.

Do not edit `.ox-agent/skills`, `schemas`, `workflows`, `pipelines`,
`lifecycle`, or `integrations` in place — they are materialized assets
tracked by `assets.lock`. `kind: core` in a skill manifest means bundled
with the distribution and sync-owned (repaired on `init --repair --sync`);
`kind: extension` means added post-init and never reconciled. To customize a
core skill, fork it under a new directory name.
<!-- ox-agent:end -->

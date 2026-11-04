# Contributing to Ox-Agent

## Development prerequisites

- Bun `>= 1.2`
- Git
- A checkout of this repository

## Development commands

```bash
bun install --frozen-lockfile
bun run build
bun test
bun run validate
```

`bun run validate` builds the CLI and runs `doctor` against the repository's
own `.ox-agent/` state.

## Adding a skill

Official skills live under `skills/`. Every skill must provide:

- `skill.yaml` metadata
- `SKILL.md` instructions
- `schemas/input.schema.json`
- `schemas/output.schema.json`
- `evals/evals.yaml`

Skills must use the Ox-Agent skill API and remain compatible with the declared
framework version. See [`skills/README.md`](skills/README.md).

Projects consume official skills or a pinned Ox-Agent fork. Do not add
project-specific overrides to the target repository's `.ox-agent/skills/`.

## CI

GitHub Actions runs the same core checks as local development:

- `bun install --frozen-lockfile`
- `bun run build`
- `bun test`
- `bun run validate`
- CLI smoke testing in a temporary project

CI intentionally does not install remote skills or invoke LLMs.

## Documentation changes

Keep the root README product and end-user focused. Put detailed CLI behavior in
[`docs/cli.md`](docs/cli.md), and keep contributor instructions here.

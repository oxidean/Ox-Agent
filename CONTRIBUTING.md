# Contributing to Ox-Agent

## Development prerequisites

- Bun `>= 1.4`
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
- Package-content validation
- CLI smoke testing in a temporary project

CI intentionally does not install remote skills or invoke LLMs.

## Package release

Use the **Prepare release** GitHub Actions workflow from the `main` branch and
select `patch`, `minor`, or `major`. The workflow calculates the next strict
SemVer version, validates the repository and package, commits the version bump,
validates the release commit, creates the matching annotated tag, and pushes
both the commit and tag.

Release commits must use this exact format:

```text
v0.2.0: release 0.2.0
```

The tag must match the package version:

```text
package.json: 0.2.0
tag:        v0.2.0
```

The prepare workflow publishes `@oxidean/ox-agent` and creates the GitHub
release in the same run after pushing the tag. The tag-triggered
`.github/workflows/release.yml` remains available for manual recovery of an
existing tag; GitHub does not start a new workflow from events emitted with the
default `GITHUB_TOKEN`.

The release workflows require `contents: write` and `packages: write`.

To run the same checks locally before manually creating a release:

```bash
bun install --frozen-lockfile
bun run build
bun test
bun run validate
bun run validate-package
bun scripts/release-version.mjs patch  # modifies package.json; do not run casually
bun scripts/validate-release.mjs HEAD v0.2.0
```

## Documentation changes

Keep the root README product and end-user focused. Put detailed CLI behavior in
[`docs/cli.md`](docs/cli.md), and keep contributor instructions here.

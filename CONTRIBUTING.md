# Contributing to Ox-Agent

## Development prerequisites

- Go 1.26 or newer
- Git
- A checkout of this repository

## Development commands

```bash
go test ./...
go vet ./...
go build ./cmd/ox-agent
```

The CLI embeds the asset tree under `cmd/ox-agent/assets/`. When changing
skills, schemas, workflows, or pipelines, update the embedded assets and test a
fresh temporary project:

```bash
tmp=$(mktemp -d)
go build -o "$tmp/ox-agent" ./cmd/ox-agent
(cd "$tmp" && ./ox-agent init && ./ox-agent doctor)
rm -rf "$tmp"
```

## Distribution model

Ox-Agent is a globally installed Go binary. Target repositories do not install
Ox-Agent through npm, Bun, or project dependencies. The binary materializes and
validates project-local `.ox-agent/` skills, workflows, pipelines, schemas, and
work-item state.

## Adding a skill

Official skill assets live under `cmd/ox-agent/assets/skills/` in the standalone
binary distribution and are mirrored from the canonical `skills/` definitions.
Every skill must provide:

- `skill.yaml` metadata
- `SKILL.md` instructions
- `schemas/input.schema.json`
- `schemas/output.schema.json`
- `evals/evals.yaml`

Skills must use the Ox-Agent skill API and remain compatible with the declared
framework version. Projects consume official skills or a compatible fork; do
not patch installed project skills in place.

## CI

Go CI runs:

- `go test ./...`
- `go vet ./...`
- `go build ./cmd/ox-agent`

Release CI builds cross-platform archives, generates checksums, and publishes
GitHub Release assets. The shell and PowerShell installers verify checksums
before installing a binary.

## Releases

Tag the commit to release on `main` with an annotated `v*.*.*` tag and push it:

```bash
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

The tag triggers `.github/workflows/release.yml`, which builds the Go binary,
creates archives for supported platforms, generates checksums, and creates the
GitHub Release. To republish an existing tag, first delete the published
release (`gh release delete <tag> --yes`, which keeps the tag), then dispatch
`release.yml` manually with the tag as input.

## Documentation changes

Keep the root README product and end-user focused. Put detailed CLI behavior in
`docs/cli.md`, installation details in `docs/install.md`, and release details in
`docs/releasing.md` as those documents are added.

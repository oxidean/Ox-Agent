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

<!--
Title: type(scope): subject — e.g. `feat(cli): add input wait`.
Squash-merge uses the PR title as the commit subject on main.
-->

## Summary

<!-- What this changes and why. Link issues (e.g. "Fixes #123") or the work item it belongs to. -->

## Changes

-
-

## Test plan

<!-- Check what you actually ran. CI runs the full matrix: go test (ubuntu + windows), go vet, go build, and the embedded-asset drift check. -->

- [ ] `go test ./...`
- [ ] `go vet ./...` and `go build ./cmd/ox-agent`
- [ ] `./scripts/check-assets.sh` — required when canonical or embedded assets change
- [ ] Verified against a temp project (`init` → `doctor` → affected commands)
- [ ] Checksum or release changes tested end-to-end
- [ ] Not run — explain below

## Checklist

- [ ] Behavior changes include tests
- [ ] No secrets committed (`.env`, tokens, keys)
- [ ] `.ox-agent/` compatibility and append-only event semantics preserved
- [ ] New or changed skills include metadata, SKILL.md, input/output schemas, and evals (see [AGENTS.md](AGENTS.md))
- [ ] Docs updated where behavior, config, or commands changed (README, [docs/cli.md](docs/cli.md), [CHANGELOG.md](CHANGELOG.md))

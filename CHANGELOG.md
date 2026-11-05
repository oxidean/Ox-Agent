# Changelog

All notable changes to Ox-Agent are documented here. This project uses
semantic versioning; 0.x releases may break compatibility between minor
versions.

## [0.1.0] - 2026-11-05

First feature-complete release of the standalone Go distribution.

### Added

- **Runtime surface** — `init`/`doctor`/`start`/`status`/`transition`/`advance`/`archive`/`unblock`, `state replay`/`explain`, `context index`/`compile`, `run` dispatch + `collect`, pipelines (`plan`, `review`, `ship`), `discuss`, `eval`, `update`, `uninstall`, shell completion.
- **Skills surface** — `skills list`, `skills check`, and `skills update` inspect, contract-validate, and re-materialize the embedded skill set; project-local skills are first-class and never reconciled.
- **`events`** — top-level journal viewer with work-item and `--type` filtering plus `--json` passthrough (replaces `state events`).
- **Harness dispatch** — `manual` adapter (packet handoff to any agent) and `spawn-cli` adapter (command templates with `{packet}`/`{output}`/`{run_dir}`); unknown adapters fail explicitly. New adapters are added per concrete consumer.
- **Contracts** — 17 embedded JSON schemas; doctor validates every project file, lifecycle edges, pipeline references, stage-skill mappings, binary compat, and the asset lock (`--fix` repairs).
- **`init --integrate`** — materializes `.agents/skills` + `.claude/skills` symlinks and a managed `AGENTS.md` block.
- **Scaffold** — seeded `.gitignore` (runtime/), `.gitattributes` (`merge=union` for the event journal and discussion threads), `context/project.yaml`, and the asset lock.
- **Provenance** — `work_item.transitioned` events landing on `shipped` record `git_branch` and `git_commit`.

### Changed

- `init` derives `framework.required_version` from the running binary's minor line instead of a hardcoded pin.
- `install.sh` resolves the latest tag via the `releases/latest` redirect (no api.github.com rate limit); `install.ps1` updated for parity and preserves `REG_EXPAND_SZ` PATH entries.

### Security

- Release `checksums.txt` is signed keylessly with cosign; the Sigstore bundle publishes as `checksums.txt.sigstore.json`.

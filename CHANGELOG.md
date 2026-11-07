# Changelog

All notable changes to Ox-Agent are documented here. This project uses
semantic versioning; 0.x releases may break compatibility between minor
versions. Patch releases cut automatically on merge to main ship with
generated release notes; this file carries curated entries for notable
work and minor-release summaries.

## [Unreleased]

## [0.1.3] - 2026-11-07

### Added

- **Durable human-input requests** — `next_action.type: await_input` files a
  request under `.ox-agent/state/input/` and leaves the run pending instead
  of blocking the turn, so async hosts (chat, messengers) can answer minutes
  or hours later. New `input` command: `request`, `list`, `respond`, `wait`,
  `cancel`.
- `input.scoped` journal events prove request/work-item ownership across
  backfills and retries.

### Fixed

- `webhook` adapter negotiates HTTP/2, dials every resolved loopback address
  (a `localhost` endpoint no longer fails when it listens on only one
  family), and routes every failure path through run-failure recording —
  no run can strand `pending` on a transport error.

## [0.1.2] - 2026-11-06

### Added

- **`skills install`/`uninstall`** — fetch skill packages from git remotes
  pinned to a resolved commit SHA, with per-file provenance in
  `.ox-agent/remote-skills.lock` verified by `skills check` and `doctor`.
- **`webhook` harness adapter** — POSTs the packet to an HTTP(S) endpoint;
  `async` leaves the run pending for file-contract output, `sync` stores
  the response body as output for schema-validated collection.
- **Snapshot-accelerated `state replay`** — resumes folding from a
  persisted snapshot when the journal prefix is intact; `--full` forces a
  from-scratch fold.

### Changed

- All 13 skill evals exercise their own contract, and SKILL.md files
  declare the `eval.*` quality flags each skill self-reports.
- CI tests run on Windows as well as Linux.

## [0.1.1] - 2026-11-06

### Added

- **Pickup hardening** — `.ox-agent/README.md` on init, doctor warns when
  harness discovery wiring is missing, namespaced `ox-agent/` skill refs
  normalize everywhere, `next_action.type` is an enforced contract enum,
  per-skill SKILL.md consumes/produces contracts, write confinement for
  materialized assets.
- **`init --repair --sync` and update-time asset sync** — `ox-agent update`
  re-executes the new binary to reconcile `.ox-agent` assets to the
  updated embedded tree; repair can rewrite drift, drop stale owned
  files, and fix the `required_version` pin.
- **Auto-tagging** — every merge to main tags the next patch version and
  dispatches the release.

### Security

- Asset-lock keys are path-confined (non-local keys dropped, symlink
  escapes rejected) so a tampered lock cannot steer reads or removals.

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

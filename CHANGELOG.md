# Changelog

All notable changes to Ox-Agent are documented here. This project uses
semantic versioning; 0.x releases may break compatibility between minor
versions. Patch releases cut automatically on merge to main ship with
generated release notes; this file carries curated entries for notable
work and minor-release summaries.

## [Unreleased]

### Fixed

- **Materialized assets lose executable file modes** — `embed.FS` drops
  mode bits, so `init`/`init --repair`/`skills update`/`skills install`
  wrote every file `0644` and bundled scripts (e.g.
  `clean-user-facing-text/scripts/*.py`,
  `remove-ai-marks/scripts/oxidean-watermarks-service.sh`) shipped
  non-executable. `sync-assets.sh` now emits
  `internal/assets/executables.txt` recording canonical exec bits; every
  write path restores `0755`/`0644` from it, remote installs preserve the
  checkout's normalized mode, doctor gains an `asset modes` check, and
  `init --repair` restores modes even when content is unchanged (POSIX
  only — Windows file modes are synthetic). (#43)

- **Input/work-item lock race** — replaced the lock-file protocol
  (O_EXCL create + stale-mtime reclaim) with kernel file locks via a new
  `internal/filelock` package (`flock` on unix, `LockFileEx` on Windows).
  The old stat-then-rename window let a reclaimer steal a freshly created
  lock and strand the real holder — observed as duplicate `IN-0001`
  allocation in a concurrent create. Kernel locks release on descriptor
  close, so crashes and slow holders need no stale-window heuristics, and
  lock files now live in gitignored `runtime/locks/`. Also fixes the
  work-item allocation lock's bare-remove reclaim and its
  delete-on-return cleanup that could remove a replacement lock. (#39)

- **`clean-user-facing-text` backup fallback mode** — when `src.stat()`
  fails, `backup_path` now falls back to `0o600` (owner-only) instead of
  the umask default, so an unexpected success in the read path can't
  produce a group/other-readable backup. Adds a stdlib-unittest
  regression suite for the vendored script helpers, exercised in CI. (#34)

## [0.1.7] - 2026-11-07

### Fixed

- **Lifecycle gate bypass** — `blocked → "*"` let a work item route
  `shipping → blocked → shipped` without the human gate and could resurrect
  archived items. The blocked return edge is now the symbolic
  `blocked → blocked_from`, resolved against the item's recorded origin;
  wildcard targets are no longer supported (`to: "*"` fails schema
  validation and never matches at runtime). (#30)
- **Plan pipeline** — `plan.yaml` now includes a `multi-stage-plan` step at
  the planning stage that `validate` depends on, so `verify` receives the
  plan artifact it checks instead of running with no upstream output. (#31)
- **Code-review output contract** — `verdict` is now coupled to
  `next_action.type` (`needs_rework` can no longer advance a stage),
  findings require a severity rank and a `file:line` citation, artifacts are
  typed, and `eval` rejects undeclared or non-boolean quality flags. (#32)

## [0.1.6] - 2026-11-07

### Added

- **Text-hygiene skills** — `clean-user-facing-text` and `remove-ai-marks`
  now ship in the embedded skill set (utility skills: enabled by default,
  unmapped from stages). They were previously vendored ad hoc into
  `.ox-agent/skills/` downstream; the toolkit is now their managed source.
- Vendored script checks: `internal/assets` tests exercise the embedded
  Python tooling (binary guards, backup/mode handling, Unicode caps) and the
  service pin verification end-to-end; CI also parse-checks `skills/*/scripts`.

### Fixed (CodeRabbit on downstream vendors, applied at source)

- `common.py` — ASCII-magic signatures (`BM`, `RIFF`, `OTTO`, `GIF8`, `wOFF*`,
  `OggS`, `BZh`, `8BPS`, `%PDF-`) validate container structure before refusing
  text (BMW/BMI/BM25/RIFF/OTTO-prefixed prose is no longer refused); stdout
  uses `surrogateescape` so undecodable bytes round-trip; in-place writes
  preserve the existing file's mode and xattrs; `.bak` handling refuses
  symlinked/non-regular targets and always backs up current input under the
  next free `.bakN` slot.
- `score_stylometry.py` — `--force-text` flag parity with `inspect`/`clean`.
- `text_unicode.py` — flag tag sequences restricted to recognized RGI
  subdivision flags (tag-encoded text now strips instead of passing);
  consecutive same-script invisibles capped (Mongolian FVS 1, Khmer inherent
  vowels 1, Hangul fillers 2) with excess reported as `excess_script_glue`.
- `oxidean-watermarks-service.sh` — cloned checkout verified against a
  trusted commit pin (`WATERMARKS_PIN_COMMIT`) before any service code runs.
- Docs: Gemini API text output **is** SynthID-watermarked (the cited forum
  answer was corrected by its author) — vendor notes, detectors reference,
  and SKILL.md corrected; `uncalibrated` density tier now has an explicit
  rewrite rule.

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

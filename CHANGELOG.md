# Changelog

All notable changes to Ox-Agent are documented here. This project uses
semantic versioning; 0.x releases may break compatibility between minor
versions. Patch releases cut automatically on merge to main ship with
generated release notes; this file carries curated entries for notable
work and minor-release summaries.

## [Unreleased]

### Fixed

- **CodeRabbit review hardening (OxL findings round 6)** — `Collect`
  checks BOTH outcome event types on replay AND before any
  validation-failure append, so a `skill.completed` journaled before an
  `output.json` rewrite cannot double the run as `skill.failed`; run-scoped input requests dedupe on
  question+options so a drifted question refuses instead of stacking a
  second request. `clean_text.py --in-place` falls back to `O_RDONLY`
  when `O_RDWR` is denied — an already-clean read-only file passes as a
  no-op again — caps the backup reread at `MAX_INPUT_BYTES`, derives
  backup permissions from the bound descriptor, and no longer lets a
  failed `close()` mask the clean exit-2 contract. `lock_inplace_fd`
  retries only genuine contention errnos (ENOLCK surfaces immediately)
  and takes the `msvcrt` lock even on empty files — LockFile permits
  ranges beyond EOF, and skipping exclusion let a torn read pass. The
  watermarks helper passes the owner's native Windows PID to the
  watchdog when `/proc` exposes it, detects zombie owners via
  `/proc`/`ps`, sizes the supervisor cap off `WATERMARKS_LOCK_SECONDS`,
  and propagates hold-removal failures instead of reporting
  `LEFT_RUNNING`. Docs restore the full
  `WATERMARKS_SERVER_API_KEY` variable name and the `.ox-agent/pipelines/`
  fork path. (#53)

- **CodeRabbit review hardening (OxL findings round 5)** — `Collect`
  reloads the run under the per-item lock before acting, so a stale
  pre-queue snapshot cannot apply an already-collected outcome a second
  time; a journaled outcome is now authoritative — a retry whose
  `output.json` drifted post-journal is refused and the run restored to
  `pending` rather than applying an outcome the journal never saw — and
  error paths return the run so reconcile warnings still surface.
  `clean_text.py --in-place` opens the target `O_RDWR | O_BINARY`
  (byte-exact on Windows), serializes the whole read/backup/rewrite
  critical section on an inode-bound `flock` — or `msvcrt.locking` on
  Windows — reads backup bytes through
  the bound descriptor so a parent-directory swap cannot back up the
  wrong file, treats `close()` write-back errors as suspect, and refuses
  hard-linked inputs whose siblings would be silently rewritten. The
  watermarks lifecycle lock now uses `flock(1)` with a `python3` helper
  where it is absent (`fcntl` on POSIX, `msvcrt.locking` plus a
  `ctypes`-based parent liveness check on Windows — `os.kill(pid, 0)`
  maps to `TerminateProcess` there, not an existence check) — replacing
  the mkdir fallback whose rename-claim reclaim had a residual race —
  bounded by `WATERMARKS_LOCK_SECONDS`.
  Docs correct the pipeline-fork recipe: the internal `name:` must match
  the filename stem. (#53)

- **CodeRabbit review hardening (OxL findings round 2)** — `Collect` now
  records a pending lifecycle event on the run when `skill.failed`
  journaling fails and replays it on a later collect, keeping the journal
  recoverable; `clean_text.py --in-place` reports `Path.resolve()`
  failures (e.g. symlink loops) as status 2 instead of a traceback; the
  watermarks service gains authenticated health checks
  (`Authorization: Bearer $WATER...Y`), per-lease holds
  under `tmp/watermarks-service.leases/<token>/` so workflows sharing a
  parent no longer cancel each other, `run` leases owned by the helper
  PID, atomic lease writes that fail `ensure` on error, conservative
  owner checks that distinguish inaccessible procfs from confirmed-dead
  owners (zombie detection, PID-reuse protection via start time, `ps`
  fallback), and a lifecycle lock that fails safe on timeout — `flock(1)`
  primary with a `python3` kernel-lock helper where it is absent (the
  interim mkdir fallback was replaced; see round 5 above). `generate-core-skills.mjs`
  emits the full invocation contract, packet-path operating rules, and
  per-skill `## Evaluation` sections, and only bootstraps
  `skill.yaml`/schemas/evals for new skills — regenerating no longer
  clobbers curated content. Docs clarify `kind` semantics, pipeline
  forking (copy the pipeline, point it at the forked skill, invoke with
  `--pipeline`), and that changing an in-flight item's stage mapping can
  repeat already-collected stages. (#53)

- **CodeRabbit review hardening (OxL findings round 3)** —
  `clean_text.py --in-place` now binds the read, backup, and rewrite to
  one inode (open fd at resolve time), so a parent-directory swap
  mid-run cannot redirect the overwrite onto an unbacked-up file — the
  in-place write is no longer atomic, so a failed rewrite retains the
  `.bak` as the recovery copy. The watermarks helper hardens further:
  `BASHPID` falls back to `$$` for Bash 3.2 (macOS), lease owner
  lifetimes are recorded TZ-independent (`TZ=UTC ps -o lstart`) with a
  legacy-format comparator so pre-fix leases are not misreaped, live
  pre-holds file leases migrate into the per-token dir as `hold-legacy-*`
  instead of blocking `ensure`, `ensure` publishes `lease-hold=<name>` on
  stderr for `WATERMARKS_HOLD`-targeted teardowns (a repeated teardown
  releases nothing rather than eating a sibling's hold), hold selection
  prefers the current owner incarnation over pid-only matches, stale
  `.hold.tmp.*` publication files are reaped after a grace period, and
  the lock fallback switched from a reclaimable mkdir lockdir to the
  `python3` kernel-lock helper described in round 5 — kernel locks need
  no reclaim protocol, so a waiter can never delete a successor's lock.
  `docs/architecture.md` notes that forked pipelines need a unique
  `name:` to avoid sharing progress state. (#53)

- **CodeRabbit review hardening (OxL findings round 4)** — `Collect`
  now serializes per work item on a kernel file lock, and pending-event
  reconciliation is idempotent (it checks the journal for a matching
  event type + `run_id` before replaying) and best-effort per run, so
  one run's stuck marker cannot block a sibling's collection — failures
  surface as `ReconcileWarnings` on stderr instead of aborting. Failure
  outcomes now save the failed run *before* journaling `skill.failed`,
  and a successful run whose outcome application fails is restored to
  `pending` so the next collect retries it safely. `ox-agent workitem
  unblock` files a durable approval request on a human-gated blocked
  edge like the other lifecycle commands instead of returning the bare
  gate error. The watermarks helper kills a spawned server and drops
  its hold when the ownership state write fails (never reporting
  `STARTED` on an unrecorded server), and `run` no longer exports
  `LEASE_OWNER_PID` into the payload environment — a payload child
  cannot write holds owned by a pid that exits while they are in use.
  Docs clarify that pipeline YAML step skills are independent of
  `stage_skills`: customize a pipeline by forking its definition, not
  by remapping stages. (#53)

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

- **`clean_text.py --in-place` on symlinked input** — resolving the input
  path before backup+write means the `.bak` lands beside the real target
  and the write no longer trips the symlink refusal that used to crash
  with an orphaned `.bak`. A failed write now also removes the `.bak` it
  just created and exits 2 cleanly instead of a traceback. (#46)

- **Watermarks service teardown scoped to owning run** —
  `oxidean-watermarks-service.sh` now files leases as per-token
  directories of holds (`tmp/watermarks-service.leases/<token>/`, keyed
  by `WATERMARKS_RUN_TOKEN`, default `$PPID`) so a second run's `teardown`
  no longer kills the service out from under the run that started it.
  Dead-owner leases are reaped conservatively, and both `ensure` and
  `teardown` always serialize on a kernel lock — `flock(1)` where
  available, otherwise a python3 helper holding the same lock; a lock
  timeout fails the call. (#47)

- **Skill `kind:` semantics documented** — `core` = bundled with the
  distribution and sync-owned by `assets.lock`; `extension` = added
  post-init and never reconciled. Clarified in the manifest schema,
  `docs/architecture.md`, and `AGENTS.md`. (#48)

- **Verdict/next_action coupling in cleaning skills** —
  `clean-user-facing-text` and `remove-ai-marks` output schemas now carry
  the same `allOf` if/then coupling as `code-review`: `needs_rework` can
  no longer advance a stage. (#49)

- **Packets carry resolved work-item paths** — `work_item_dir` and
  `artifacts_dir` are now compiled into every packet (dispatch creates
  the artifacts dir), so skills never glob-derive `WI-0005*` and produce
  a literal-star directory that breaks Windows checkouts. `collect`
  rejects declared artifact paths containing glob/Windows-invalid
  characters, traversal, or trailing-dot/space segments — failing like a
  schema error so the run is re-dispatchable. All 15 bundled SKILL.mds
  document the contract. (#52)

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

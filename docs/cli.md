# CLI reference

Ox-Agent is a globally installed Go binary. Run it from any repository after
installing the release binary. Commands that touch project state operate on
the nearest `.ox-agent/` directory.

## Project commands

### `ox-agent init`

Create `.ox-agent/` with the embedded official skills, schemas, workflows,
pipelines, lifecycle definitions, and policy. Non-destructive — it refuses to
overwrite an existing `.ox-agent/` unless `--repair` is passed.

- `ox-agent init --repair` — restore missing scaffold files and directories in
  an existing `.ox-agent/` without touching existing content.
- `ox-agent init --repair --sync` — additionally reconcile the project with the
  running binary: rewrite asset files that drifted from the embedded tree,
  remove owned files this binary no longer ships (untouched copies only), and
  reset `framework.yaml`'s `required_version` when it excludes the binary.
  Drifted files the lock never owned — local extensions colliding with
  embedded paths — are kept and reported, never overwritten. The asset
  lock is always refreshed.
- `ox-agent init --integrate` — additionally wire agent-facing access: create
  `.agents/skills` symlinked to `.ox-agent/skills`, `.claude/skills` pointing at
  it, and a managed block in `AGENTS.md`. Idempotent.

### `ox-agent doctor`

Validate the project: schema-check every YAML/JSON under `.ox-agent/`, verify
framework compatibility and semver pins, stage-skill mappings, pipeline
references, integrations, lifecycle edges, and the asset lock. Reports all
issues. `--fix` repairs fixable problems (missing directories, stale locks).

## Work item commands

### `ox-agent start <title>`

Create a work item in lifecycle state `intake`. Returns an id like `WI-0001`.

### `ox-agent status [work-item]`

Without arguments, list active work items. With an id, show the full record:
state, profile, current stage, blocked_from, and artifacts.

### `ox-agent transition <work-item> <state>`

Move a work item along a legal lifecycle edge. Illegal transitions are
rejected with the list of legal targets. Human-gated edges require the gate
to be satisfied first.

### `ox-agent advance <work-item>`

Advance the work item to the next stage in its profile. `intake` items
bootstrap into the first stage.

### `ox-agent archive <work-item>` / `ox-agent unblock <work-item>`

Archive a work item, or return a `blocked` item to the state recorded in
`blocked_from`.

### `ox-agent run <work-item>`

Dispatch the skill mapped to the work item's current stage through the
configured harness integration (`--harness`, `--skill`, `--stage`,
`--budget` to override). With `--pipeline <name>`, dispatch every ready
pipeline step instead, bounded by `policy.budgets.max_parallel_agents`.

The manual adapter writes the packet under `runtime/runs/<id>/packet.json`,
prints the skill documentation path, and waits for
`runtime/runs/<id>/output.json`.

Supported adapters are `manual`, `spawn-cli`, and `webhook`; new adapters
are added per concrete consumer, never as generic placeholders. A `spawn-cli`
integration shells a command template — `{packet}`, `{output}`, and
`{run_dir}` expand to paths under the run directory — and works for any
agent CLI, including remote dispatch (`ssh host agent ...`). Example, saved
as `.ox-agent/integrations/myagent.yaml`:

```yaml
schema_version: 1
name: myagent
adapter: spawn-cli
command: "myagent run --packet {packet} --write-output {output}"
timeout_seconds: 600
description: Dispatch runs to the myagent CLI.
```

A `webhook` integration POSTs the packet JSON to an HTTP(S) endpoint — the
packet contract stays the interface; the adapter owns transport only. In
`async` mode (default) a 2xx acknowledgement leaves the run pending and the
remote harness writes `output.json` back through the same file contract. In
`sync` mode a non-empty 2xx response body is stored as `output.json`; collect
still schema-validates it, so a malformed response fails as a contract
violation. A bearer token comes from the environment variable named by
`token_env` — never from the yaml. Example:

```yaml
schema_version: 1
name: fleet
adapter: webhook
endpoint: https://agents.internal/ox-agent/dispatch
mode: async
token_env: FLEET_TOKEN
timeout_seconds: 60
description: Dispatch packets to the managed agent fleet.
```

### `ox-agent run collect <run-id>`

Validate a run's `output.json` against the skill's output schema, journal the
result, and apply `next_action`. Invalid output marks the run failed.

A run ends in one of three statuses:

- `collected` — the output was applied. Collected runs count toward skill
  selection, stage completion, and pipeline progress.
- `retry` — the output signalled unfinished work (`verdict: needs_rework`,
  `next_action.type: retry`, an `await_input` request, or a blocked run that
  also requested rework or retry). The skill stays dispatchable and does not
  count as done; a blocking outcome still transitions the work item to
  `blocked`, while `await_input` leaves it in place and records the filed
  request on the run (`awaiting_input: IN-NNNN`, echoed by `run collect`).
- `failed` — schema validation failed or the skill reported `status: failed`.

Completion checks use only the latest run per skill or pipeline step: a
collected run superseded by a newer retry no longer counts, so the skill is
re-selected after the item is unblocked.

### `ox-agent review <work-item>` / `ox-agent ship <work-item>`

Shortcuts for `run --pipeline review` and `run --pipeline ship`.

### `ox-agent discuss`

Durable discussion threads under `.ox-agent/discussions/`:

```text
ox-agent discuss open <title>
ox-agent discuss list
ox-agent discuss show <thread>
ox-agent discuss add <thread> <message>
ox-agent discuss summarize <thread> <conclusion>
ox-agent discuss close <thread> [conclusion]
```

Threads keep an append-only message log; `summarize` bumps the summary
version and marks covered messages; `close` resolves the thread.

### `ox-agent input`

Durable human-input requests under `.ox-agent/state/input/` — the answer
to async hosts (chat agents, CI) where a synchronous prompt would expire
before a human sees it. File the question, end the turn; the human answers
whenever they respond.

```text
ox-agent input request --kind clarification --question "..." [--work-item WI-0001]
ox-agent input list [--status pending] [--work-item WI-0001]
ox-agent input respond IN-0001 --by <name> --answer "..."
ox-agent input respond IN-0001 --by <name> --approve | --reject
ox-agent input wait IN-0001 [--interval 5s --factor 2 --max-interval 60s --timeout 0]
# async agents: bound the wait to a turn budget and re-poll next turn —
# `input wait IN-0001 --timeout 20s` exits 0 with "pending" when the budget
# lapses; timeout 0 (the default) holds until an answer, for daemons only.
ox-agent input cancel IN-0001 --by <name>
```

Kinds: `clarification` (free-text answer), `decision` (answer must match a
declared `--option`), `approval` (carries a pending `transition`/`archive`
action — `respond --approve` executes it through the normal lifecycle path
with the responder recorded as approver, `--reject` declines).

Requests are filed automatically when they are needed: a `human`-gated
`transition`/`archive`/`advance`/`run` leaves an approval request naming
the pending action (`respond IN-.... --by <you> --approve` completes it),
a run collected with a blocked/escalated outcome leaves a clarification
request carrying the reason, and `next_action.type: await_input` files a
clarification (or decision, when `next_action.options` is given) without
blocking the item. `wait` polls with exponential backoff so agents never
hot-loop or expire a synchronous ask — a bounded `--timeout` expiring
reports the request as still `pending` rather than failing, so async
agents can poll once per turn instead of holding a blocking wait.
Requests do not expire unless filed with `--ttl`. Answered requests are
folded into later packets under `context.inputs`.

## State commands

### `ox-agent state replay <work-item>`

Rebuild current work-item state by replaying the event journal. When a
snapshot exists under `state/snapshots/` and its `last_event_id` still
marks an intact journal prefix, replay resumes from it and folds only
newer events (`--full` forces a from-scratch fold). `--snapshot` persists
the result; `--json` prints it.

### `ox-agent state explain <work-item>`

Print the journal events that produced the current state.

### `ox-agent events [work-item]`

Inspect the project event journal — the audit spine every command appends
to. With a work item (id or directory name) only its events print.
`--type <prefix>` filters by event-type prefix (e.g. `work_item`), `--json`
emits the raw journal lines.

Snapshots are written under `.ox-agent/state/snapshots/` and journal
`snapshot.written` events.

## Context commands

### `ox-agent context index`

Build the context index over `.ox-agent/context/` and `.ox-agent/discussions/`.

### `ox-agent context compile <work-item>`

Compile a token-budgeted context packet for `--skill` at the work item's
current stage. Prints to stdout, or writes to the `.ox-agent`-relative path
given by `-o/--output`. Journals `context.compiled`.

## Skills

### `ox-agent skills list`

List every materialized skill under `.ox-agent/skills/` with its version,
enabled flag, drift status against the embedded distribution
(`current`/`modified`/`incomplete`/`project`), and binary compatibility.
Directories the embedded tree does not cover are project-local skills;
remote-installed skills show `remote` status and their pinned
`source@sha` in the SOURCE column.

### `ox-agent skills install <git-url> [--ref r] [--path subdir] [--name n] [--force]`

Fetch a skill package (a directory containing `skill.yaml` + `SKILL.md`)
from a git remote and materialize it under `.ox-agent/skills/<name>`. The
manifest is validated against the skill-manifest contract before anything
is written. Provenance is recorded in `.ox-agent/remote-skills.lock`: the
source URL, requested `--ref` (branch/tag/commit, default remote HEAD),
the resolved commit SHA, and a sha256 digest of every installed file — so
`skills check` and `doctor` verify what was reviewed is what runs.

- Embedded (ox-agent-owned) skill directories can never be shadowed.
- An existing directory is replaced only when the lock already owns it and
  `--force` is passed — that re-pins to the newly resolved SHA.
- Package symlinks resolving outside the package are rejected.
- Remote skills are never touched by `skills update` (embedded-only);
  upgrade is `install <same-source> --force`.

Wire the skill into `config.yaml` (`skills.enabled`, `stage_skills`) to use
it in stages. Review the pinned commit before trusting a remote skill —
provenance guarantees integrity, not intent.

### `ox-agent skills uninstall <name>`

Remove a remote-installed skill directory and its lock entry. Embedded and
project-local skills are refused.

### `ox-agent skills check`

Validate each materialized skill: `skill.yaml` against the manifest schema,
declared input/output schemas present and compilable, `SKILL.md` present,
eval suite present, the `compatibility.ox_agent` pin satisfied by the
running binary, and remote skills verified against their pinned digests in
`remote-skills.lock` (drifted or missing files report as findings). Exits
non-zero on any error finding.

### `ox-agent skills update [--yes]`

Re-materialize embedded skill files that are missing or drifted from the
binary's embedded copy, and refresh the asset lock. Missing files are always
restored; drifted files are only overwritten with `--yes` (otherwise listed
as skipped). Project-local skills are never touched.

## Administration

### `ox-agent update`

Verify the latest release checksum and atomically replace the installed
binary. `--check` reports installed vs latest without changing anything;
`--version <tag>` selects a specific release; `--binary-only` skips project
reconciliation.

When run inside a project, `update` then re-executes the freshly installed
binary's `init --repair --sync` so ox-agent-owned assets (skills, schemas,
workflows, pipelines, lifecycle, integrations) move to the new embedded
tree and a stale `required_version` pin is reset — the running process
still carries the old assets in memory, so the sync has to come from the
new binary. Outside a project the asset sync is skipped; `skills update`
remains the targeted skills-only repair for local drift.

### `ox-agent uninstall --yes`

Remove the installed binary. Project `.ox-agent/` directories are never
touched.

### `ox-agent eval <skill>`

Score a skill's recorded eval outputs against its output schema and declared
quality assertions, and report pass rates against `thresholds`. Exits
non-zero below threshold or when cases are unrecorded. `--emit` writes a
packet for every case lacking an output, to
`.ox-agent/runtime/evals/<skill>/<case-id>.packet.json`; record results as
`<case-id>.output.json` next to it.

### `ox-agent version` / `ox-agent completion`

Print the build version, or emit shell completion scripts.

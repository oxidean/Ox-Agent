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

Supported adapters are `manual` and `spawn-cli`; new adapters are added per
concrete consumer, never as generic placeholders. A `spawn-cli` integration
shells a command template — `{packet}`, `{output}`, and `{run_dir}` expand to
paths under the run directory — and works for any agent CLI, including remote
dispatch (`ssh host agent ...`). Example, saved as
`.ox-agent/integrations/myagent.yaml`:

```yaml
schema_version: 1
name: myagent
adapter: spawn-cli
command: "myagent run --packet {packet} --write-output {output}"
timeout_seconds: 600
description: Dispatch runs to the myagent CLI.
```

### `ox-agent run collect <run-id>`

Validate a run's `output.json` against the skill's output schema, journal the
result, and apply `next_action`. Invalid output marks the run failed.

A run ends in one of three statuses:

- `collected` — the output was applied. Collected runs count toward skill
  selection, stage completion, and pipeline progress.
- `retry` — the output signalled unfinished work (`verdict: needs_rework`,
  `next_action.type: retry`, or a blocked run that also requested rework or
  retry). The skill stays dispatchable and does not count as done; a blocking
  outcome still transitions the work item to `blocked`.
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

## State commands

### `ox-agent state replay <work-item>`

Rebuild current work-item state by replaying the event journal.

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
Directories the embedded tree does not cover are project-local skills.

### `ox-agent skills check`

Validate each materialized skill: `skill.yaml` against the manifest schema,
declared input/output schemas present and compilable, `SKILL.md` present,
eval suite present, and the `compatibility.ox_agent` pin satisfied by the
running binary. Exits non-zero on any error finding.

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

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

### `ox-agent run collect <run-id>`

Validate a run's `output.json` against the skill's output schema, journal the
result, and apply `next_action`. Invalid output marks the run failed.

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

### `ox-agent state events [work-item]`

List raw journal events, optionally filtered to one work item.

Snapshots are written under `.ox-agent/state/snapshots/` and journal
`snapshot.written` events.

## Context commands

### `ox-agent context index`

Build the context index over `.ox-agent/context/` and `.ox-agent/discussions/`.

### `ox-agent context compile <work-item>`

Compile a token-budgeted context packet for `--skill` at the work item's
current stage. Prints to stdout, or writes to the `.ox-agent`-relative path
given by `-o/--output`. Journals `context.compiled`.

## Administration

### `ox-agent update`

Verify the latest release checksum and atomically replace the installed
binary. `--check` reports installed vs latest without changing anything;
`--version <tag>` selects a specific release.

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

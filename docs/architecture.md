# Architecture

How Ox-Agent fits together — the model every contract, command, and file
exists to serve.

## The execution boundary

```text
binary   = how execution works   (deterministic, global, versioned)
skill    = what a role does      (SKILL.md + schemas, repository-local)
workflow = when capabilities apply
pipeline = how work is ordered
work     = what happened          (event journal, work items)
harness  = who executes           (manual | spawn-cli | webhook)
```

The binary never reasons about the work. It resolves configuration, enforces
schemas, sequences stages, journals events, and hands packets to a harness.
The harness — any agent that can read a file and write a file — does the
reasoning and returns a structured output.

## Where things live

```text
~/.local/bin/ox-agent          the only global artifact

<repo>/.ox-agent/
├── framework.yaml             binary + schema compat (required_version)
├── config.yaml                project config: workflow profile, stage_skills
├── policy.yaml                autonomy + budgets (max_parallel_agents)
├── lifecycle/workflow.yaml    states + legal transition edges + gates
├── skills/                    materialized skill packages (owned by binary)
│   └── <skill>/{skill.yaml, SKILL.md, schemas/, evals/}
├── workflows/                 profiles mapping stages -> skills
├── pipelines/                 DAGs of skill steps (plan, review, ship)
├── integrations/              harness configs (manual.yaml default)
├── context/                   standards, decisions, project.yaml, index
├── work/{active,archived}/    work items: index.json + artifacts
├── state/
│   ├── events.ndjson          append-only journal — the state spine
│   ├── snapshots/             folded state checkpoints
│   └── input/                 durable human-input requests
├── discussions/               append-only hash-chained threads
├── integrations/              (see above)
└── runtime/                   generated packets, runs, cache (gitignored)
```

Canonical asset trees at the repo root (`skills/`, `schemas/`, `workflows/`,
`pipelines/`) are mirrored into `internal/assets/embedded/` by
`scripts/sync-assets.sh`; CI rejects drift. `init` materializes the embedded
tree into `.ox-agent/` and writes `assets.lock` — a hash manifest the binary
uses to distinguish ox-owned files (safe to repair) from project-local or
remote-installed skills (never reconciled).

## The work loop

```text
start        create work item -> state: intake
run          resolve stage skill -> compile packet -> dispatch to harness
             (dispatch writes runtime/runs/<id>/ and returns)
collect      validate output.json against the skill's output schema,
             journal the result, apply next_action
advance      move to the next profile stage when the skill says so
review/ship  run the review/ship pipelines (DAGs, parallel-aware)
transition   any legal lifecycle edge; gates may require --by approver
archive      terminal: move work/active -> work/archived
```

A **packet** (`runtime/packets/<wi>/<run>.json`) is the harness contract:
`{skill, work_item_id, context, constraints, output: {path, schema},
prior_outputs}`. The harness writes `output.json` at the declared path;
`collect` validates it against `schemas/output.schema.json` and honors
`next_action` (`advance_stage | retry | escalate | await_input | complete`).

**Await input** is the async-chat path: instead of blocking, `run` files a
durable request under `state/input/`, leaves the run pending, and exits —
a messenger-style host can answer minutes or hours later via `input answer`,
then `run collect` resumes.

## The event journal

`state/events.ndjson` is append-only and committed (`.gitattributes` applies
`merge=union`). Every mutation journals an event:
`work_item.created | transitioned | shipped`, `skill.dispatched | collected`,
`pipeline.started | step_ready | completed`, `input.requested | scoped |
answered`, `discussion.*`, `snapshot.written`, `eval.completed`, …

`state replay` folds the journal into current state; `--snapshot` persists the
fold and later replays resume from it when the journal prefix is verifiably
intact (`--full` forces a from-scratch fold). `events` is the read surface.

## Harness model

`integrations/<name>.yaml` declares an adapter:

| adapter    | dispatch semantics |
|------------|--------------------|
| `manual`   | prints packet path + expected output path; any agent completes it |
| `spawn-cli`| executes a command template (`{packet}`/`{output}`/`{run_dir}`) |
| `webhook`  | POSTs the packet; `async` = 2xx leaves pending, `sync` = body is output |

Adapters are added per concrete consumer — never generically. The packet
files are the interface; transport is pluggable.

## Versioning and updates

- `framework.required_version` is derived from the binary's minor line at
  `init --repair --sync` time; `doctor` enforces it.
- `ox-agent update` replaces the binary (checksum-verified), then re-execs it
  to sync `.ox-agent` assets to the new embedded tree (`--binary-only` skips).
- Merges to `main` auto-tag the next patch release (`tag-release.yml`) and
  dispatch `release.yml` (GoReleaser + cosign keyless signing).

## Provenance and confinement

- `assets.lock` — embedded-asset hashes; only ox-owned files are repaired.
- `remote-skills.lock` — `skills install` pins every fetched file to the
  resolved commit SHA; `skills check`/`doctor` verify drift.
- All materialization writes pass through path confinement (symlinks are
  never followed outside `.ox-agent/`).

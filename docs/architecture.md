# Architecture

How Ox-Agent fits together — the model every contract, command, and file
exists to serve. This document covers the agent/harness surface — packets,
dispatch, the journal; the end-user flow lives in
[getting-started.md](./getting-started.md) and the command reference in
[cli.md](./cli.md).

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
├── assets.lock                hash manifest of ox-owned assets
├── lifecycle/workflow.yaml    states + legal transition edges + gates
├── skills/                    materialized skill packages (owned by binary)
│   └── <skill>/{skill.yaml, SKILL.md, schemas/, evals/}
├── schemas/                   input/output contract schemas (materialized)
├── workflows/                 stage-order profiles (normal.yaml)
├── pipelines/                 DAGs of skill steps (plan, review, ship)
├── integrations/              harness configs (manual.yaml default)
├── context/                   standards, decisions, specs/, project.yaml, index
├── work/{active,archived}/    the spec trail: work items + artifacts
├── state/
│   ├── events.ndjson          append-only journal — the state spine
│   ├── snapshots/             folded state checkpoints
│   └── input/                 durable human-input requests
├── discussions/               append-only hash-chained threads
└── runtime/                   generated packets, runs, evals, locks (gitignored)
```

Canonical asset trees at the repo root (`skills/`, `schemas/`, `workflows/`,
`pipelines/`, `lifecycle/`, `integrations/`) are mirrored into
`internal/assets/embedded/` by
`scripts/sync-assets.sh`; CI rejects drift. `init` materializes the embedded
tree into `.ox-agent/` and writes `assets.lock` — a hash manifest the binary
uses to distinguish ox-owned files (safe to repair) from project-local or
remote-installed skills (never reconciled).

Required-but-initially-empty directories (`work/active`, `work/archived`,
`context/standards`, `context/decisions`, `discussions`, `state/snapshots`,
`state/input`) carry a `.gitkeep` sentinel so a fresh clone keeps them —
git does not track empty directories, and `doctor` requires the committed
skeleton (a missing `state/input` is tolerated for clones older than the
sentinel convention). `runtime/` is never committed or scaffolded: it is gitignored
generated state, and every writer recreates it on demand.

A skill manifest's `kind` records its distribution channel, not its pipeline
role. `core` ships inside the distribution — canonical under `skills/` here,
embedded, materialized, and hash-tracked by `assets.lock`, so upstream fixes
propagate on `init --repair --sync`. `extension` is anything added post-init
(`skills install`, or a local directory): the lock never owns it. To
customize a core skill, fork it under a new directory name — in-place edits
to sync-owned files are reconciled away. Set the fork's manifest `kind:` to
`extension` (the lock never owns it, so `core` would misrepresent whether
upstream fixes reach it), rename the manifest `name:` to
`ox-agent/<new-name>`, and update the `skill` constants in the fork's
`schemas/input.schema.json` and `schemas/output.schema.json` — plus the
`skill:` field in `evals/evals.yaml`, since `eval` packets and recorded
results take the qualified name — to match: packets and outputs validate
against them, so a fork still naming the original skill rejects its own
results. Add the new directory name to
`skills.enabled` in `.ox-agent/config.yaml`, and replace the original
name in the relevant `skills.stage_skills` entries — otherwise stage
runs keep selecting the original. Update hard-coded asset paths in the fork's instructions too: a
copied SKILL.md that still points at the core skill's scripts executes the
core helper.

Two caveats when forking. First, `stage_skills` does not affect pipelines:
`ox-agent review`/`ship` dispatch the bundled `pipelines/*.yaml` steps by
skill name. To fork a pipeline step's skill, copy the pipeline file under
a new filename (e.g. `.ox-agent/pipelines/review-mine.yaml`), set its
internal `name:` to the filename stem (`name: review-mine`), update its
`skill:` references, and invoke it via
`ox-agent run <wi> --pipeline review-mine`. The `--pipeline` flag resolves
the file by name and progress tracks that internal `name:`, so the two
must match — a mismatched pair can fail to load at collect or load a
different pipeline if that file exists, and a fork that keeps
`name: review` shares progress state with the original and can report
its steps as already done. Second, replace a
mapping only between stages: an in-flight item that already collected the
original skill keeps that completion under the old name, so the fork does
not inherit it — the next dispatch can re-run the same unit of work under
the fork. Finish the affected stage (or start the item fresh) before
swapping the mapping.

## The work loop

```text
start        create work item -> state: intake
run          resolve stage skill -> compile packet -> dispatch to harness
             (dispatch writes runtime/runs/<id>/ and returns)
collect      validate output.json against the skill's output schema,
             journal the result, apply next_action
advance      manually move to the next profile stage
review/ship  run the review/ship pipelines (DAGs, parallel-aware)
transition   any legal lifecycle edge; gates may require --by approver
archive      terminal: move work/active -> work/archived
```

A **packet** (`runtime/runs/<run-id>/packet.json`) is the harness contract:
`{skill, work_item_id, context, constraints, output: {path, schema}}` plus
envelope fields (`schema_version`, `packet_id`, `created_at`, `stage`), with
`prior_outputs` and `inputs` nested under `context`. The harness writes
`output.json` at `output.path`, resolved against `.ox-agent/` — not the
project root the harness runs from; `collect` validates it against the
skill's `schemas/output.schema.json` and honors `next_action`
(`advance_stage | retry | escalate | await_input | complete`).

**Await input** is the async-chat path: when a skill returns `await_input`,
`run collect` files a durable request under `state/input/` and marks the
run `retry` — collection only accepts `pending` runs, so a second `collect`
on that run fails. Answer with `input respond`, then dispatch a fresh
`run` — for pipeline steps use `review`, `ship`, or `run --pipeline <name>`
instead, since a plain `run` produces a run without the pipeline and step
identifiers the pipeline counts on. The new packet carries the answer in
`context.inputs`.

## The event journal

`state/events.ndjson` is append-only and committed (`.gitattributes` applies
`merge=union`). Every domain mutation journals an event:
`work_item.created | transitioned | stage_advanced | archived`,
`skill.invoked | completed | failed`, `pipeline.completed`,
`input.requested | scoped | answered | expired | cancelled`,
`discussion.*`, `context.compiled`, `eval.completed`,
`skills.installed | uninstalled`.

`state replay` folds the journal into current state; `--snapshot` persists the
fold and later replays resume from it when the journal prefix is verifiably
intact (`--full` forces a from-scratch fold). `events` is the read surface.

## Harness model

`integrations/<name>.yaml` declares an adapter:

| adapter    | dispatch semantics |
|------------|--------------------|
| `manual`   | prints packet path + expected output path; any agent completes it |
| `spawn-cli`| executes a command template (`{packet}`/`{output}`/`{run_dir}`/`{work_item}`/`{skill}`/`{stage}`) |
| `webhook`  | POSTs the packet; `async` = 2xx leaves pending, `sync` = body is output |

Adapters are added per concrete consumer — never generically. The packet
files are the interface; transport is pluggable.

## Versioning and updates

- `framework.required_version` is derived from the binary's minor line at
  `init` and reconciled on `init --repair --sync`; `doctor` enforces it.
- `ox-agent update` replaces the binary (checksum-verified), then re-execs it
  to sync `.ox-agent` assets to the new embedded tree (`--binary-only` skips).
- Merges to `main` auto-tag the next patch release (`tag-release.yml`) and
  dispatch `release.yml` (GoReleaser + cosign keyless signing).

## Provenance and confinement

- `assets.lock` — embedded-asset hashes; only ox-owned files are repaired.
- `remote-skills.lock` — `skills install` pins every fetched file to the
  resolved commit SHA; `skills check` verifies drift.
- All materialization writes pass through path confinement (symlinks are
  never followed outside `.ox-agent/`).

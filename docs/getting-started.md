# Getting started

Ox-Agent is a repository-local AI-SDLC runtime: a single binary that
materializes skills, workflows, and contracts into `.ox-agent/`, then drives
work items through them with a journaled, schema-checked dispatch loop.

## Install

```bash
curl -fsSL https://github.com/oxidean/Ox-Agent/releases/latest/download/install.sh | bash
ox-agent version
```

On Windows (PowerShell): `irm https://github.com/oxidean/Ox-Agent/releases/latest/download/install.ps1 | iex`.

## Initialize a project

Run inside the repository you want to manage:

```bash
ox-agent init
ox-agent init --integrate   # optional: wire .agents/skills + AGENTS.md
ox-agent doctor             # → project healthy
```

`init` materializes the skill library, lifecycle, workflows, pipelines,
schemas, and policy into `.ox-agent/`. `--integrate` links
`.agents/skills` → `.ox-agent/skills` and `.claude/skills` →
`.agents/skills`, and adds a managed block to `AGENTS.md` so coding
agents discover the toolkit.
Without `--integrate` nothing outside `.ox-agent/` advertises the toolkit —
`doctor` warns when no integration is detected. On Windows, symlink creation
needs Developer Mode or an elevated shell; `integrate` reports a warning and
leaves the rest of the wiring in place when it cannot link.

The scaffold commits to git: `.ox-agent/.gitignore` keeps only generated
`runtime/` out, and `.gitattributes` marks `state/events.ndjson`
`merge=union` so the append-only journal merges cleanly across branches.
Everything else — work items, the journal, promoted specs under
`context/specs/` — is the project's spec trail and belongs in history.

## Your first work item

There are two ways to drive a work item: invoke the skills inside your
agent, or run the dispatch loop from the shell yourself. Most people do
the former; the loop below is what the agent executes on your behalf.

### In your agent

`init --integrate` linked the materialized skills where harnesses look for
them — `.agents/skills`, `.claude/skills`, and a managed `AGENTS.md`
block. Open the repository in your agent and describe the work:

```text
> /brainstorm add rate limiting to the API
```

The agent files the work item (`ox-agent start`), dispatches the
stage-mapped skill, writes its result, and collects it — the
`run`/`run collect` loop below — for every stage until the item ships.
When a skill needs a decision it files a durable request instead of
blocking your chat; answer whenever:

```bash
ox-agent input list
ox-agent input respond IN-0001 --by <you> --answer "..."
```

### The dispatch loop (what the agent runs)

```bash
ox-agent start "Add rate limiting to the API"
# → WI-0001: Add rate limiting to the API
```

The work item enters `intake`. The first `run` bootstraps it into the
profile's first stage and dispatches the mapped skill:

```bash
ox-agent run WI-0001
# run_... dispatched (adapter: manual)
#   packet:    .ox-agent/runtime/runs/run_.../packet.json
#   skill doc: .ox-agent/skills/discover-context/SKILL.md
#   write result to: .ox-agent/runtime/runs/run_.../output.json
```

With the default `manual` adapter, hand `packet.json` and the skill doc to
any agent — or work the skill yourself — then write the skill's output JSON
to `output.json` and collect it:

```bash
ox-agent run collect run_...
# → validated against the skill's output schema, journaled, next_action applied
```

`collect` is the contract boundary: output must match the skill's
`schemas/output.schema.json` or the run is marked failed. When every skill
mapped to the current stage is collected, the work item auto-advances;
`ox-agent advance WI-0001` moves between stages explicitly, and
`ox-agent transition WI-0001 shipped --by <you>` crosses the human-gated ship
edge (the journal records your git branch and commit as provenance). Without
`--by`, the command files a durable approval request and exits non-zero
with the request handle in the message — `ox-agent input pending` shows
it and `ox-agent input respond IN-0001 --by <you> --approve` completes
the transition whenever you answer. Automation should treat that exit as
"waiting on the human", not a failed request — the same surface catches
run escalations, so an async-chat agent never has to hold a synchronous
prompt open.

Loop until shipped:

```bash
ox-agent status WI-0001    # full record
ox-agent events WI-0001    # the journal so far
ox-agent state explain WI-0001
```

## Pipelines

`review` and `ship` run the bundled pipelines — DAGs of skill steps that
dispatch every ready step up to `policy.budgets.max_parallel_agents`:

```bash
ox-agent review WI-0001    # dispatched: review → run_... (code-review)
ox-agent run pending       # anything awaiting output.json
ox-agent ship WI-0001      # verify → ship steps in dependency order
```

Keep invoking the pipeline command and collecting runs until it prints
`pipeline <name> complete`.

## Maintaining the skill set

```bash
ox-agent skills list       # every materialized skill: enabled, drift, compat
ox-agent skills check      # contract-validate every skill
ox-agent skills update     # restore missing embedded files; --yes overwrites drift
```

Drop a directory with a `skill.yaml`, `SKILL.md`, and `schemas/` into
`.ox-agent/skills/` and it becomes a project-local skill — `skills list`
shows it as `project`, `skills check` contract-validates it, and
`skills update` never touches it.

`ox-agent update` upgrades the binary and, when run inside a project,
re-executes the new binary's repair so every ox-agent-owned asset moves to
the embedded tree that ships with it (use `--binary-only` to skip). For a
full reconcile without a binary change, run `ox-agent init --repair --sync`.

## Dispatching to an agent CLI

The `manual` adapter works with anything that can read a file. To dispatch
directly, add a `spawn-cli` integration
(`.ox-agent/integrations/myagent.yaml`):

```yaml
schema_version: 1
name: myagent
adapter: spawn-cli
command: "myagent run --packet {packet} --write-output {output}"
timeout_seconds: 600
```

`{packet}`, `{output}`, and `{run_dir}` expand to run-directory paths; the
command runs with the project root as working directory. Remote execution
works the same way — `ssh buildbox /opt/run-skill {packet}` — as long as the
remote side writes `output.json` back before `collect`. See
[cli.md](cli.md) for the full command reference.

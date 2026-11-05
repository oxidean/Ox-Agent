# Ox-Agent project state

This directory is managed by the ox-agent CLI — a repository-local AI-SDLC
runtime (https://github.com/oxidean/Ox-Agent).

- Contracts (managed assets, do not edit in place): skills/, schemas/,
  workflows/, pipelines/, lifecycle/, integrations/ — tracked by assets.lock.
- Project state: framework.yaml, config.yaml, policy.yaml, work/,
  state/events.ndjson (append-only journal), discussions/, context/.
- Generated runtime (gitignored): runtime/ packets and run outputs.

Drive work items with 'ox-agent start|status|transition|run|collect' and
validate the setup with 'ox-agent doctor'. For agent-harness wiring
(AGENTS.md block + .agents/skills discovery), run 'ox-agent integrate'.

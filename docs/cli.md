# CLI reference

Ox-Agent is a globally installed Go binary. Run it from any repository after
installing the release binary.

## `ox-agent version`

Print the installed binary version.

## `ox-agent init`

Create the repository-local `.ox-agent/` directory. The command materializes
the official skills, schemas, workflows, and pipelines embedded in the binary.
It is non-destructive and refuses to overwrite an existing `.ox-agent/`.

## `ox-agent doctor`

Validate the local framework configuration and required repository-local
structure. It also reports the installed binary version.

## `ox-agent start <title>`

Create a work item under `.ox-agent/work/active/`. The current foundation
creates the work-item record; lifecycle execution is implemented incrementally.

## `ox-agent status`

List active work items.

## Planned commands

The following commands are part of the standalone roadmap:

```text
ox-agent update
ox-agent update --check
ox-agent state replay <work-item>
ox-agent state explain <work-item>
ox-agent context compile <work-item>
ox-agent run <work-item>
ox-agent review <work-item>
ox-agent ship <work-item>
```

# CLI reference

The CLI is available as `bun node_modules/ox-agent/dist/cli.js` after installing
a local Ox-Agent checkout in a project. Once Ox-Agent is published, the
shorter `bunx ox-agent` form can be used. During local development, run
`bun dist/cli.js` from the Ox-Agent checkout.

## Project commands

### `init`

Create `.ox-agent/` in the current directory. The command is non-destructive
and refuses to overwrite an existing directory.

### `doctor`

Validate the supported project configuration, policy, installed skill manifests,
evaluation manifests, and discussion summaries. Exits non-zero when validation
fails.

### `start <title>`

Create a work item under `.ox-agent/work/active/` and append a creation event.
Phase 0 creates the record only; it does not execute the work item.

### `status [work-item]`

List active and archived work items. The optional identifier uses prefix
matching, for example `bunx ox-agent status WI-0001`.

## Skill commands

### `skills list`

List installed skills and their metadata.

### `skills install [source] [--skill <name>]`

Delegate skill installation to the external `npx skills` CLI. If no source is
provided, the source configured in `.ox-agent/config.yaml` is used. If no skill
is selected, the enabled skills from project configuration are requested.

Example:

```bash
bunx ox-agent skills install /path/to/ox-agent --skill brainstorm
```

### `skills check`

Check for available updates through `npx skills`. A non-zero result indicates
that updates are available or the check failed.

### `skills update [--yes]`

Update skills through `npx skills`. Use `--yes` in non-interactive environments.
Updates should be reviewed before applying them to active work.

## Exit behavior

The CLI exits non-zero when a command cannot complete, the project root cannot
be found, validation fails, or the delegated skills command fails.

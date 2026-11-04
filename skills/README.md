# Ox-Agent skills

Official Ox-Agent skills live here. Each skill package must contain:

- `skill.yaml` metadata
- `SKILL.md` instructions
- `schemas/input.schema.json`
- `schemas/output.schema.json`
- `evals/evals.yaml`

Projects consume these skills through a pinned Ox-Agent source or fork. They must not patch installed skills locally.

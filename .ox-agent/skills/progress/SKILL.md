---
name: ox-agent/progress
description: Record compact, structured progress updates and derive resumable work-item status from lifecycle events.
---

# progress

Record compact, structured progress updates and derive resumable work-item status from lifecycle events.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

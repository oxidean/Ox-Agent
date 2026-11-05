---
name: ox-agent/discover-context
description: Discover relevant project standards, architecture, ADRs, code patterns, tests, and constraints for the current work item.
---

# discover-context

Discover relevant project standards, architecture, ADRs, code patterns, tests, and constraints for the current work item.

## Operating rules

- Load only the current work-item packet and referenced artifacts.
- Preserve evidence and disagreement rather than inventing certainty.
- Do not exceed the permissions or paths in the input contract.
- Return a compact structured result matching the output schema.
- Promote durable decisions and blockers into the appropriate work-item artifact.

# Discovery — spec enforcement outcomes in status

## Existing behavior

- This checkout runs with `specs.enforcement: strict`.
- The only indexed spec is `context/specs/README.md`; there is no promoted product spec yet.
- Collection evaluates declared `spec_impact` and coverage findings. It can hold a passing run for a durable decision request under strict enforcement.
- `ox-agent status <work-item>` reports lifecycle fields and the five latest event names. It does not render spec findings or summarize the current spec-enforcement state.
- Collection has a transient `SpecWarnings` surface, while spec events are journaled and can be inspected with `events` or `state explain`.

## Relevant spec of record

`context/specs/README.md` governs how specs are promoted, indexed, delivered in packets, and committed. This work item should preserve that model; it is about making its enforcement outcomes easier to inspect, not changing the policy semantics.

## Discovery conclusion

The useful follow-up is observability, not a second enforcement mechanism: determine which durable record is authoritative for status (journal events, input requests, or both), then expose concise current findings without duplicating or recomputing the collector coverage logic. There are no existing project-specific specs that define a status presentation contract.

# Phase 4: Remote Push - Context

**Gathered:** 2026-02-23
**Status:** Ready for planning/execution

<domain>
## Phase Boundary

Implement scheduled and shutdown push delivery for unsynced local events:
- batch read unsynced rows across trace/error/metric tables
- JSON payload delivery to configurable endpoint
- retry with jitter backoff on failure
- mark rows synced only after confirmed success
- split payloads above size threshold

</domain>

<decisions>
## Locked Decisions

- Push endpoint is `OCT_PUSH_ENDPOINT`; empty disables scheduler.
- Push interval defaults to `OCT_PUSH_INTERVAL` (5m).
- Payload must carry `trace_id` per event.
- Only mark rows as synced on HTTP 200.
- On shutdown: worker drains first, then final push attempt executes before DB close.
- Payload size cap default is 5MB and must split large sends.

## Claude's Discretion

- Internal payload envelope shape.
- Retry parameters and jitter implementation details.
- Max rows fetched per push cycle.

</decisions>

---

*Phase: 04-remote-push*
*Context gathered: 2026-02-23*

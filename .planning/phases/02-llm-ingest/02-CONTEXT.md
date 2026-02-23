# Phase 2: LLM Ingest - Context

**Gathered:** 2026-02-23
**Status:** Ready for planning/execution

<domain>
## Phase Boundary

Implement ingest pipeline for LLM traces and error events:
- `POST /v1/traces`
- `POST /v1/errors`
- fire-and-forget HTTP behavior (`202` quickly)
- single persist worker writing SQLite in batches via buffered channel

No system metrics collector, no remote push scheduler, no log parser in this phase.

</domain>

<decisions>
## Locked Decisions

- Endpoint paths are fixed:
  - `POST /v1/traces`
  - `POST /v1/errors`
- Ingest queue capacity is fixed at `512`.
- Persist worker is single-writer and is the only SQLite write path.
- Batch policy is fixed: up to `50` events or `500ms` flush window.
- Every stored ingest row has generated `trace_id` (UUID v4).
- Text truncation is required using `OCT_MAX_TEXT_BYTES`.
- Fire-and-forget contract:
  - handler responds `202` immediately when event accepted or dropped due queue saturation
  - handler never waits for SQLite persistence
- Drop behavior:
  - when queue is full, event is dropped and `events_dropped` counter increments
  - process must remain stable and non-blocking
- Existing `/health` contract fields remain unchanged.

## Claude's Discretion

- Event type struct/package layout
- Validation strictness on optional fields
- Exact batched SQL insert implementation style
- How to represent event kind in worker channel payload

</decisions>

<specifics>
## Specific Ideas

- Keep one ingest event envelope type internally (`kind=trace|error`) to avoid duplicate worker loops.
- Start with synchronous JSON decode + lightweight validation in handlers.
- Keep trace/error insert SQL prepared and reused by worker loop where possible.
- Add focused tests for:
  - `202` response behavior
  - truncation behavior
  - queue saturation drop behavior
  - persistence appearing within batch window

</specifics>

<deferred>
## Deferred Ideas

- Metrics ingestion (`system_metrics`) stays in Phase 3.
- Remote push writeback (`pushed_at` updates from delivery) stays in Phase 4.
- Log parsing enrichment stays in Phase 5.

</deferred>

---

*Phase: 02-llm-ingest*
*Context gathered: 2026-02-23*

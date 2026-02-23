# Phase 3: System Metrics - Context

**Gathered:** 2026-02-23
**Status:** Ready for planning/execution

<domain>
## Phase Boundary

Implement container-scoped system metrics collection and storage maintenance:
- cgroup-aware CPU and memory metrics
- disk usage for the data volume
- disk I/O rate capture
- ingestion into existing ingest channel
- cleanup and WAL checkpoint background loops

No remote push logic (Phase 4) and no log parsing (Phase 5).

</domain>

<decisions>
## Locked Decisions

- Metrics must flow through the same ingest queue/worker path as trace/error events.
- CPU sample #1 is discarded to avoid false near-100% startup reading.
- Metrics interval defaults to 15s and is environment-configurable.
- Maintenance responsibilities in this phase:
  - cleanup old synced rows when DB or disk thresholds are exceeded
  - background WAL checkpoint loop, RESTART checkpoint when WAL exceeds threshold
- Keep `/health` contract unchanged.

## Claude's Discretion

- Exact env variable names for new metric/maintenance tunables.
- Metadata payload details for system metric rows.
- Cleanup threshold defaults and retention enforcement strategy.

</decisions>

<deferred>
## Deferred Ideas

- System/process-level deep profiling remains hardening work.
- Push scheduler and row syncing semantics remain Phase 4.

</deferred>

---

*Phase: 03-system-metrics*
*Context gathered: 2026-02-23*

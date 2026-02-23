# Phase 5: Log Parsing - Context

**Gathered:** 2026-02-23
**Status:** Ready for planning/execution

<domain>
## Phase Boundary

Add optional log parsing mode that:
- activates only when `OCT_LOG_PATH` is set
- tail-follows appended lines
- classifies gateway/channel/config signals into enrichment events
- emits through existing ingest queue
- handles log rotation by reopening the new file

</domain>

<decisions>
## Locked Decisions

- Optional mode: no log parser goroutine should run when `OCT_LOG_PATH` is empty.
- Parsed events must reuse existing ingest channel/worker path.
- Rotation handling is required.

## Claude's Discretion

- Poll interval and parsing heuristics.
- Mapping parsed lines into persisted event shape.

</decisions>

---

*Phase: 05-log-parsing*
*Context gathered: 2026-02-23*

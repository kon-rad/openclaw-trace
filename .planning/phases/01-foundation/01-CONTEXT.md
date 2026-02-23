# Phase 1: Foundation - Context

**Gathered:** 2026-02-23
**Status:** Ready for planning

<domain>
## Phase Boundary

A static Go binary (CGO_ENABLED=0) that boots, reads configuration from environment variables, opens SQLite with WAL mode and correct pragmas, creates four tables, exposes GET /health, and shuts down cleanly on SIGTERM. This is the skeleton — no ingest endpoints, no metrics collection, no push pipeline. Those are Phase 2-4.

</domain>

<decisions>
## Implementation Decisions

### Configuration naming & defaults
- Env var prefix: `OCT_` (OpenClaw Trace)
- Default port: `9090` (OCT_PORT)
- Default DB path: `/data/openclaw-trace.db` (OCT_DB_PATH)
- Default log level: `info` (OCT_LOG_LEVEL)
- Push endpoint: `OCT_PUSH_ENDPOINT` — if empty, log warning on startup and disable push (don't refuse to start)
- Push interval: `OCT_PUSH_INTERVAL` — default `5m`
- Retention: `OCT_RETENTION_DAYS` — default `3` days
- Text truncation: `OCT_MAX_TEXT_BYTES` — configurable limit for LLM I/O text
- Env vars only — no config file support. Standard for containers.
- Binary supports `--help` (lists all env vars with defaults) and `--version` flags

### Health endpoint contract
- Path: `GET /health` (not /healthz)
- Always returns HTTP 200 — health status conveyed via JSON `status` field
- Response fields (all four groups):
  - Core: `status` (ok/degraded/error), `uptime_seconds`, `version`
  - DB: `db_status` (ok/error), `db_size_bytes`, `wal_size_bytes`
  - Queue: `queue_depth` (ingest channel), `events_received`, `events_dropped`
  - Push: `last_push_time`, `last_push_status`, `unsynced_count`

### Startup & shutdown logging
- **Startup prints (in order):**
  1. Banner with version: `"openclaw-trace v0.1.0 starting..."`
  2. Config summary: all resolved env vars and their values (mask sensitive values)
  3. DB init status: `"SQLite opened at /data/openclaw-trace.db (WAL mode, 4 tables)"`
  4. Listening message: `"Listening on :9090"`
- **Shutdown prints:**
  1. Signal received: `"SIGTERM received, shutting down..."`
  2. Drain progress: `"Draining ingest channel: 42 events remaining..."`
  3. Push flush result: `"Final push: 87 events synced"` or `"Final push failed: connection refused"`
  4. Final stats: `"Shutdown complete. Total events: 12,345. Uptime: 4h23m"`
- **Log format:** JSON always (one JSON object per line). No human-readable mode. Parseable by Fly.io log drains.

### SQLite table schema
- **trace_id format:** UUID v4 string (36-char)
- **Timestamps:** Unix milliseconds (INTEGER) — fast sorting, compact
- **LLM text storage:** Inline TEXT columns (input_text, output_text) in llm_traces table — simple, one table
- **Metadata:** Every data table has a `metadata` TEXT column for arbitrary JSON — future-proof, extensible
- **Sync tracking:** Two columns on every data table: `synced` BOOLEAN (for fast WHERE) + `pushed_at` INTEGER (Unix ms, for audit)
- **Indexes:** Composite index on `(synced, created_at)` per data table — covers the main push query
- **Retention:** Default 3 days (OCT_RETENTION_DAYS). Volume-aware cleanup triggers earlier if disk usage exceeds 80%.
- **Four tables:** llm_traces, error_events, system_metrics, push_log

### Claude's Discretion
- Exact Go package layout (cmd/, internal/, etc.)
- SQLite cache_size pragma value
- Exact health check JSON field names (camelCase vs snake_case)
- Ingest channel buffer size tuning
- Signal handling implementation details
- How --help formats the env var listing

</decisions>

<specifics>
## Specific Ideas

- `OCT_` prefix chosen for brevity and distinctiveness — short enough to type, namespaced enough to avoid collisions
- Health endpoint always 200 because this is an internal sidecar, not a load-balanced service — monitors should check the JSON status field
- JSON logging only (no text mode) because this runs in containers where logs are always parsed by aggregators
- 3-day retention (not 7) because the 1GB Fly.io volume is shared with OpenClaw and needs headroom

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 01-foundation*
*Context gathered: 2026-02-23*

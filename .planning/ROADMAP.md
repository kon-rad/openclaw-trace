# Roadmap: openclaw-trace

**Created:** 2026-02-23
**Depth:** Standard
**Phases:** 6
**Requirements:** 36 mapped

## Phase Overview

| # | Phase | Goal | Requirements | Success Criteria |
|---|-------|------|--------------|------------------|
| 1 | Foundation | Static binary boots, reads config, opens SQLite with correct pragmas, exposes health endpoint, shuts down cleanly | FOUN-01, FOUN-02, FOUN-03, FOUN-04, FOUN-05, STOR-01, STOR-02, STOR-05 | 5 |
| 2 | LLM Ingest | HTTP ingest endpoints accept LLM traces and error events fire-and-forget, single persist worker writes batches to SQLite | LLMT-01, LLMT-02, LLMT-03, LLMT-04, LLMT-05, ERRC-01, ERRC-02, ERRC-03, ERRC-04, ERRC-05, STOR-03, STOR-04 | 5 |
| 3 | System Metrics | Cgroup-aware CPU/RAM/disk/I-O collection feeds the ingest channel; volume-aware cleanup and WAL checkpoint keep DB healthy | SYSM-01, SYSM-02, SYSM-03, SYSM-04, SYSM-05, SYSM-06, STOR-06, STOR-07 | 4 |
| 4 | Remote Push | Batch reader assembles JSON payloads, HTTP push client sends with retry/backoff, push scheduler runs every 5 min | PUSH-01, PUSH-02, PUSH-03, PUSH-04, PUSH-05, PUSH-06, PUSH-07, PUSH-08 | 5 |
| 5 | Log Parsing | Optional tail-follow mode reads OpenClaw gateway logs, extracts enrichment events, feeds them through the standard ingest channel | LOGP-01, LOGP-02, LOGP-03, LOGP-04, LOGP-05 | 3 |
| 6 | Hardening | Binary size and RSS verified under CI, integration tests cover the full pipeline end-to-end, GoReleaser produces release artifacts | (validation phase — no new requirements, cross-cuts all prior phases) | 4 |

---

## Phase 1: Foundation

**Goal:** A static Go binary compiles with CGO_ENABLED=0, reads all configuration from environment variables, opens SQLite with WAL mode and correct pragmas, exposes GET /health, and shuts down cleanly on SIGTERM draining any in-flight work before closing the database.

**Requirements:**
- FOUN-01: Single static Go binary with zero external dependencies (CGO_ENABLED=0)
- FOUN-02: Configuration via environment variables with sane defaults (port, push interval, push endpoint, db path, log level)
- FOUN-03: Graceful shutdown: drain ingest channel, attempt final push, checkpoint WAL, close DB on SIGTERM
- FOUN-04: Health check endpoint (GET /health) returning db status, queue depth, uptime, last push time
- FOUN-05: Structured JSON logging via slog with configurable log level
- STOR-01: SQLite database at configurable path (default /data/openclaw-trace.db) with WAL mode
- STOR-02: Four tables: llm_traces, error_events, system_metrics, push_log
- STOR-05: SQLite pragmas: WAL mode, busy_timeout=10000, auto_vacuum=INCREMENTAL, appropriate cache_size

**Success Criteria:**
1. `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` succeeds and produces a self-contained binary with no shared library dependencies (verified with `ldd`).
2. Binary starts with only environment variables set (no config file required), opens SQLite at the configured path, and returns HTTP 200 from GET /health with a valid JSON body containing db_status, queue_depth, uptime_seconds, and last_push_time.
3. Sending SIGTERM causes the process to exit cleanly within 15 seconds: ingest channel drains, WAL is checkpointed, database is closed without corruption (verified by re-opening the DB after process exit).
4. All log output is valid JSON (one object per line) and honors the LOG_LEVEL environment variable (debug/info/warn/error).
5. SQLite file opened in WAL mode confirms `PRAGMA journal_mode` returns `wal`, `PRAGMA busy_timeout` returns 10000, and `PRAGMA auto_vacuum` returns 2 (INCREMENTAL).

**Dependencies:** None (first phase)

---

## Phase 2: LLM Ingest

**Goal:** POST /v1/traces and POST /v1/errors accept structured events and immediately return 202. A single persist worker goroutine owns all SQLite writes, batching up to 50 events per transaction with a 500ms window fallback. The ingest channel acts as the decoupling buffer between HTTP handlers and the writer.

**Requirements:**
- LLMT-01: HTTP endpoint (POST /v1/traces) accepts LLM call trace events and returns 202 immediately
- LLMT-02: Captures: provider, model, input_text, output_text, prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms, status, error_type
- LLMT-03: Each trace assigned a unique trace_id (UUID) at ingest for idempotency
- LLMT-04: Configurable text truncation limit (TRACE_MAX_TEXT_BYTES) to prevent DB bloat from large context windows
- LLMT-05: Fire-and-forget semantics: agent never blocks waiting for tracer response
- ERRC-01: HTTP endpoint (POST /v1/errors) accepts structured error events and returns 202 immediately
- ERRC-02: Captures: error_type (llm_error, crash, system_error), message, stack_trace, severity, metadata JSON
- ERRC-03: Error types include: LLM API failures, rate limits, timeouts, malformed responses
- ERRC-04: Error types include: agent process crashes, OOM kills, unhandled exceptions
- ERRC-05: Error types include: system errors (disk full, network failures, permission denied)
- STOR-03: Single-writer pattern: one goroutine owns all SQLite writes via buffered channel (cap=512)
- STOR-04: Batch inserts: persist worker batches up to 50 events or 500ms window per transaction

**Success Criteria:**
1. POST /v1/traces with a well-formed JSON body returns HTTP 202 in under 5ms on loopback; the trace row appears in llm_traces within 600ms (next batch window).
2. POST /v1/traces with an input_text exceeding TRACE_MAX_TEXT_BYTES stores a truncated value; the stored byte length equals TRACE_MAX_TEXT_BYTES exactly.
3. Each stored row in llm_traces has a non-null, unique trace_id (UUID v4); sending the same payload twice produces two rows with different trace_ids.
4. POST /v1/errors with error_type values of llm_error, crash, and system_error all persist correctly; the metadata JSON field round-trips without loss.
5. Sending 600 events in rapid succession to a full ingest channel (cap=512) causes the HTTP handler to return 202 for the first 512 and drop-count subsequent ones without blocking or panicking; the process remains stable.

**Dependencies:** Phase 1 (SQLite, graceful shutdown, config)

---

## Phase 3: System Metrics

**Goal:** A background goroutine collects cgroup-aware CPU, RAM, disk usage, and I/O rates on a configurable ticker and writes them into the same ingest channel. A separate goroutine checkpoints the WAL periodically. Volume-aware cleanup deletes old synced rows before disk usage reaches a critical threshold.

**Requirements:**
- SYSM-01: CPU usage collection on a configurable ticker interval (default 15s)
- SYSM-02: CPU metrics are cgroup-aware (read /sys/fs/cgroup/ for container-scoped values, not host /proc/stat)
- SYSM-03: Memory usage collection: RSS, available, total (cgroup-aware)
- SYSM-04: Disk usage collection: total, used, free, usage percentage for /data volume
- SYSM-05: Disk I/O rates: read/write bytes per interval
- SYSM-06: First CPU sample discarded (always reads ~100% due to zero baseline)
- STOR-06: Volume-aware cleanup: delete old synced rows when DB exceeds size threshold or disk usage exceeds 80%
- STOR-07: Background WAL checkpoint goroutine (every 10 min, RESTART if WAL exceeds 50MB)

**Success Criteria:**
1. system_metrics rows are inserted at the configured interval (default 15s); the first row in each process run is never the first sample (it is discarded), so no row shows cpu_percent near 100% at startup.
2. On a Fly.io Linux container (cgroup v2), the recorded cpu_percent reflects the container cgroup quota — not the sum across all host CPUs — confirmed by comparing the value against `cat /sys/fs/cgroup/cpu.stat`.
3. When synthetic disk usage is pushed above 80% of the /data volume (via a test file), the cleanup routine deletes enough old synced rows within one cleanup cycle to bring usage below the threshold; the DB remains readable and uncorrupted after cleanup.
4. The WAL checkpoint goroutine runs every 10 minutes and forces a RESTART checkpoint when the WAL file exceeds 50MB; after the checkpoint, the WAL file size drops below 1MB.

**Dependencies:** Phase 2 (persist worker, ingest channel)

---

## Phase 4: Remote Push

**Goal:** A push scheduler goroutine fires every 5 minutes, reads all unsynced rows from all tables into a JSON batch (capped at 5MB with splitting for oversized batches), POSTs to the configured remote endpoint with exponential backoff and full jitter, and marks rows as synced only on confirmed HTTP 200. A final flush runs during graceful shutdown before the database closes.

**Requirements:**
- PUSH-01: Periodic batch push to configurable remote API endpoint (TRACE_PUSH_ENDPOINT env var)
- PUSH-02: Default push interval 5 minutes, configurable via TRACE_PUSH_INTERVAL env var
- PUSH-03: Batch reader: SELECT unsynced rows across all tables, assemble JSON payload
- PUSH-04: JSON batch payload with trace_id for each event (enables remote deduplication)
- PUSH-05: Exponential backoff with full jitter on push failure (cenkalti/backoff)
- PUSH-06: Mark rows as synced (pushed_at timestamp) only on confirmed HTTP 200
- PUSH-07: Final flush attempt on graceful shutdown before DB close
- PUSH-08: Push payload size cap (5MB default) with splitting for large batches

**Success Criteria:**
1. When TRACE_PUSH_ENDPOINT points to a test HTTP server, unsynced rows from llm_traces, error_events, and system_metrics all appear in the push payload; after a successful push, every pushed row has a non-null pushed_at timestamp in SQLite.
2. When the test server returns 500, the push client retries with exponential backoff and full jitter; rows remain unsynced after all retries; subsequent push cycles pick up and retry the same rows.
3. When a batch exceeds the 5MB size cap, it is split into multiple payloads; each payload is sent and acknowledged independently; all rows are eventually marked synced.
4. Sending SIGTERM during an active push cycle completes the in-flight push (or retries up to the shutdown timeout), marks rows synced if confirmed, then closes the database cleanly.
5. Every event in the push payload includes a trace_id field; the same row is never included in two push payloads (because pushed_at is set on the first confirmed delivery).

**Dependencies:** Phase 2 (SQLite rows), Phase 1 (graceful shutdown integration)

---

## Phase 5: Log Parsing

**Goal:** An optional mode — activated by setting TRACE_LOG_PATH — tail-follows the OpenClaw gateway log file, parses new lines for channel events, gateway errors, and config changes, and emits enrichment events into the standard ingest channel so they are stored and pushed alongside traces.

**Requirements:**
- LOGP-01: Optional mode: parse OpenClaw gateway log files for additional context
- LOGP-02: Configurable log file path (TRACE_LOG_PATH env var)
- LOGP-03: Tail-follow mode: read new lines as they're appended
- LOGP-04: Extract enrichment data: channel events, gateway errors, config changes
- LOGP-05: Parsed events flow into the same ingest channel as HTTP events

**Success Criteria:**
1. When TRACE_LOG_PATH is not set, the binary starts and operates normally with no log-parsing goroutine running (verified via goroutine count in /health or pprof).
2. When TRACE_LOG_PATH points to a file that has lines appended during a test, the log parser emits one enrichment event per parsed line into the ingest channel; those events appear in SQLite within one batch window (600ms).
3. When the log file is rotated (renamed and replaced), the tail-follower reopens the new file within one poll interval and continues emitting events without losing lines or duplicating them.

**Dependencies:** Phase 2 (ingest channel, persist worker)

---

## Phase 6: Hardening

**Goal:** Verify the binary meets all resource budget targets under realistic load, add integration tests that cover the full pipeline (ingest to SQLite to push to mock remote), and configure GoReleaser to produce release artifacts for linux/amd64, linux/arm64, and darwin. No new requirements are introduced — this phase validates all prior phases meet their non-functional constraints.

**Requirements:** (No new v1 requirements — this phase cross-cuts all prior phases to validate resource budgets and release readiness that are implicit in FOUN-01, FOUN-02, and the Constraints section of PROJECT.md)

**Success Criteria:**
1. A stripped, trimpath-compiled binary (`-s -w -trimpath`) for linux/amd64 is at most 15MB in size; CI fails the build if the binary exceeds this threshold.
2. RSS measured via `/proc/self/status` VmRSS after 10 minutes of idle operation (metrics ticker running, no LLM traces received) is at most 20MB; a push cycle with a 1000-row batch does not exceed 30MB RSS transiently.
3. An integration test starts the full binary against a real SQLite file and a `httptest.NewServer` mock push target: POSTs 100 LLM traces, waits one push cycle, and asserts all 100 rows appear in the mock server's received payloads with correct trace_ids and field values.
4. GoReleaser produces linux/amd64, linux/arm64, and darwin/amd64 binaries from a single `goreleaser release --snapshot` command; all three binaries pass a smoke test (`./openclaw-trace-linux-amd64 --help` exits 0).

**Dependencies:** All prior phases complete

---

*Created: 2026-02-23*

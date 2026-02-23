# Requirements: openclaw-trace

**Defined:** 2026-02-23
**Core Value:** Complete visibility into what an AI agent is doing, what it costs, and how healthy its host is — with zero impact on agent performance.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Foundation

- [ ] **FOUN-01**: Single static Go binary with zero external dependencies (CGO_ENABLED=0)
- [ ] **FOUN-02**: Configuration via environment variables with sane defaults (port, push interval, push endpoint, db path, log level)
- [ ] **FOUN-03**: Graceful shutdown: drain ingest channel, attempt final push, checkpoint WAL, close DB on SIGTERM
- [ ] **FOUN-04**: Health check endpoint (GET /health) returning db status, queue depth, uptime, last push time
- [ ] **FOUN-05**: Structured JSON logging via slog with configurable log level

### LLM Tracing

- [ ] **LLMT-01**: HTTP endpoint (POST /v1/traces) accepts LLM call trace events and returns 202 immediately
- [ ] **LLMT-02**: Captures: provider, model, input_text, output_text, prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms, status, error_type
- [ ] **LLMT-03**: Each trace assigned a unique trace_id (UUID) at ingest for idempotency
- [ ] **LLMT-04**: Configurable text truncation limit (TRACE_MAX_TEXT_BYTES) to prevent DB bloat from large context windows
- [ ] **LLMT-05**: Fire-and-forget semantics: agent never blocks waiting for tracer response

### Error Capture

- [ ] **ERRC-01**: HTTP endpoint (POST /v1/errors) accepts structured error events and returns 202 immediately
- [ ] **ERRC-02**: Captures: error_type (llm_error, crash, system_error), message, stack_trace, severity, metadata JSON
- [ ] **ERRC-03**: Error types include: LLM API failures, rate limits, timeouts, malformed responses
- [ ] **ERRC-04**: Error types include: agent process crashes, OOM kills, unhandled exceptions
- [ ] **ERRC-05**: Error types include: system errors (disk full, network failures, permission denied)

### System Metrics

- [ ] **SYSM-01**: CPU usage collection on a configurable ticker interval (default 15s)
- [ ] **SYSM-02**: CPU metrics are cgroup-aware (read /sys/fs/cgroup/ for container-scoped values, not host /proc/stat)
- [ ] **SYSM-03**: Memory usage collection: RSS, available, total (cgroup-aware)
- [ ] **SYSM-04**: Disk usage collection: total, used, free, usage percentage for /data volume
- [ ] **SYSM-05**: Disk I/O rates: read/write bytes per interval
- [ ] **SYSM-06**: First CPU sample discarded (always reads ~100% due to zero baseline)

### Storage

- [ ] **STOR-01**: SQLite database at configurable path (default /data/openclaw-trace.db) with WAL mode
- [ ] **STOR-02**: Four tables: llm_traces, error_events, system_metrics, push_log
- [ ] **STOR-03**: Single-writer pattern: one goroutine owns all SQLite writes via buffered channel (cap=512)
- [ ] **STOR-04**: Batch inserts: persist worker batches up to 50 events or 500ms window per transaction
- [ ] **STOR-05**: SQLite pragmas: WAL mode, busy_timeout=10000, auto_vacuum=INCREMENTAL, appropriate cache_size
- [ ] **STOR-06**: Volume-aware cleanup: delete old synced rows when DB exceeds size threshold or disk usage exceeds 80%
- [ ] **STOR-07**: Background WAL checkpoint goroutine (every 10 min, RESTART if WAL exceeds 50MB)

### Remote Push

- [ ] **PUSH-01**: Periodic batch push to configurable remote API endpoint (TRACE_PUSH_ENDPOINT env var)
- [ ] **PUSH-02**: Default push interval 5 minutes, configurable via TRACE_PUSH_INTERVAL env var
- [ ] **PUSH-03**: Batch reader: SELECT unsynced rows across all tables, assemble JSON payload
- [ ] **PUSH-04**: JSON batch payload with trace_id for each event (enables remote deduplication)
- [ ] **PUSH-05**: Exponential backoff with full jitter on push failure (cenkalti/backoff)
- [ ] **PUSH-06**: Mark rows as synced (pushed_at timestamp) only on confirmed HTTP 200
- [ ] **PUSH-07**: Final flush attempt on graceful shutdown before DB close
- [ ] **PUSH-08**: Push payload size cap (5MB default) with splitting for large batches

### Log Parsing (Optional)

- [ ] **LOGP-01**: Optional mode: parse OpenClaw gateway log files for additional context
- [ ] **LOGP-02**: Configurable log file path (TRACE_LOG_PATH env var)
- [ ] **LOGP-03**: Tail-follow mode: read new lines as they're appended
- [ ] **LOGP-04**: Extract enrichment data: channel events, gateway errors, config changes
- [ ] **LOGP-05**: Parsed events flow into the same ingest channel as HTTP events

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Custom Events

- **CEVT-01**: HTTP endpoint (POST /v1/events) for arbitrary named events with JSON metadata
- **CEVT-02**: custom_events table in SQLite
- **CEVT-03**: Included in periodic push batches

### Process Monitoring

- **PROC-01**: Running process list with per-process CPU and memory
- **PROC-02**: Zombie process detection and alerting
- **PROC-03**: OpenClaw gateway process health monitoring

### Network Metrics

- **NETW-01**: Open connection count and bandwidth usage
- **NETW-02**: DNS resolution time probes
- **NETW-03**: Network error rate tracking

### Push Resilience

- **RESL-01**: Circuit breaker after N consecutive failed push cycles
- **RESL-02**: Retry-After header respect from remote API
- **RESL-03**: Push cursor for resumable push after crashes

### Self-Observability

- **SELF-01**: pprof endpoints for Go runtime profiling
- **SELF-02**: expvar counters for internal metrics (events received, dropped, pushed)

## Out of Scope

| Feature | Reason |
|---------|--------|
| Web dashboard UI | Augmi platform dashboard handles visualization |
| Real-time WebSocket push | Periodic batch push is sufficient; complexity not justified |
| Distributed tracing (spans) | Different problem domain; this tracks a single agent |
| Log aggregation/search | Different product category (Loki, Elastic) |
| LLM evaluation/quality scoring | Platform feature requiring LLM-as-judge |
| Prompt management/versioning | Platform/UI feature |
| Windows support | Fly.io targets Linux only |
| Multi-tenant aggregation | Platform aggregates across agents |
| SDK/library packages | HTTP interface is language-agnostic, no SDK needed |
| OTLP protocol (v1) | Proto complexity for one producer; align field names now, add protocol in v2 |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| FOUN-01 | — | Pending |
| FOUN-02 | — | Pending |
| FOUN-03 | — | Pending |
| FOUN-04 | — | Pending |
| FOUN-05 | — | Pending |
| LLMT-01 | — | Pending |
| LLMT-02 | — | Pending |
| LLMT-03 | — | Pending |
| LLMT-04 | — | Pending |
| LLMT-05 | — | Pending |
| ERRC-01 | — | Pending |
| ERRC-02 | — | Pending |
| ERRC-03 | — | Pending |
| ERRC-04 | — | Pending |
| ERRC-05 | — | Pending |
| SYSM-01 | — | Pending |
| SYSM-02 | — | Pending |
| SYSM-03 | — | Pending |
| SYSM-04 | — | Pending |
| SYSM-05 | — | Pending |
| SYSM-06 | — | Pending |
| STOR-01 | — | Pending |
| STOR-02 | — | Pending |
| STOR-03 | — | Pending |
| STOR-04 | — | Pending |
| STOR-05 | — | Pending |
| STOR-06 | — | Pending |
| STOR-07 | — | Pending |
| PUSH-01 | — | Pending |
| PUSH-02 | — | Pending |
| PUSH-03 | — | Pending |
| PUSH-04 | — | Pending |
| PUSH-05 | — | Pending |
| PUSH-06 | — | Pending |
| PUSH-07 | — | Pending |
| PUSH-08 | — | Pending |
| LOGP-01 | — | Pending |
| LOGP-02 | — | Pending |
| LOGP-03 | — | Pending |
| LOGP-04 | — | Pending |
| LOGP-05 | — | Pending |

**Coverage:**
- v1 requirements: 36 total
- Mapped to phases: 0
- Unmapped: 36

---
*Requirements defined: 2026-02-23*
*Last updated: 2026-02-23 after initial definition*

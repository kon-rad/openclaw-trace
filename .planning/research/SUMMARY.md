# Research Summary: openclaw-trace

## Stack Decision

Go 1.26 (CGO_ENABLED=0, Green Tea GC) is the language. HTTP serving uses stdlib `net/http` with Go 1.22+ ServeMux pattern matching — no router framework needed for a 5-endpoint surface. SQLite is handled by `modernc.org/sqlite` v1.46.1 (pure Go, no CGo), which is the only viable choice for a static binary that cross-compiles cleanly. System metrics come from `gopsutil/v4` v4.26.1 (the only library with full coverage of CPU, RAM, disk, net, and processes). Retry logic uses `cenkalti/backoff/v5`; env config uses `sethvargo/go-envconfig`; logging uses stdlib `log/slog`. Testing uses stdlib + `stretchr/testify`. Build and release use GoReleaser v2.14.0 targeting linux/amd64, linux/arm64, and darwin. No frameworks, no CGo, no heavy indirect dependencies — binary size and supply-chain cleanliness are hard constraints from day one.

## Table Stakes Features

- SQLite local storage (WAL mode, 4 tables: llm_traces, system_metrics, custom_events, error_events)
- HTTP ingest endpoint (`POST /v1/traces`, `POST /v1/events`, `POST /v1/errors`) — fire-and-forget, returns 202 immediately
- LLM call logging with cost tracking (provider, model, prompt/completion tokens, cost_usd, latency_ms, status)
- System metrics collection on a ticker: CPU %, RAM MB, disk usage on `/data`, network I/O, process count
- Periodic remote push (default 5 min): batch all unsynced rows, POST JSON to configurable endpoint, retry with exponential backoff
- Health check endpoint (`GET /health`) returning db status and queue depth
- Graceful shutdown: drain ingest channel to SQLite, attempt final push, checkpoint WAL, close DB
- Configuration entirely via environment variables (8-12 vars with sane defaults)
- Volume-aware SQLite cleanup: delete old synced rows when DB exceeds size threshold or disk usage exceeds 80%
- Idempotency: every event gets a `trace_id` UUID at ingest; push payload includes it; remote API must be designed to deduplicate on it

## Architecture Overview

```
OpenClaw Agent (Node.js)
  │
  │  POST /v1/traces | /v1/events | /v1/errors  (fire-and-forget, localhost:9090)
  ▼
HTTP Server (stdlib net/http, :9090)
  │  validate + type incoming JSON
  │  non-blocking send to ingest channel (cap=512); drop + counter if full
  ▼
Ingest Channel (buffered chan Event)
  ▼
Persist Worker (single goroutine — sole SQLite writer)
  │  batch 50 events or 500ms window → BEGIN IMMEDIATE → INSERT → COMMIT
  ▼
SQLite: /data/openclaw-trace.db  (WAL mode, auto_vacuum=INCREMENTAL)
  │
  ├── System Collector goroutine (gopsutil ticker, 15s)
  │     pushes SystemMetric events into same ingest channel
  │
  └── Push Scheduler goroutine (ticker, 5min)
        │  SELECT unsynced rows (all 4 tables) → assemble JSON batch
        │  POST to remote API with exponential backoff + jitter
        │  On 200: UPDATE synced=1 for pushed row IDs
        │  On failure: rows stay unsynced, picked up next cycle
        ▼
      augmi.world /api/traces/ingest (or custom TRACE_PUSH_ENDPOINT)
```

Goroutine count at idle: ~8 (HTTP pool, Persist Worker, System Collector, Push Scheduler, optional Push Executor, Log Parser if enabled, Signal Handler). Single SQLite writer invariant eliminates all write concurrency issues. Separate read-only `*sql.DB` for push batch queries enables concurrent reads under WAL.

## Critical Risks

1. **SQLite BUSY errors destroy data silently** (C-1, C-2): Default busy_timeout=0 and WAL auto-checkpoint failures combine to cause SQLITE_BUSY and runaway WAL growth that can fill the 1GB `/data` volume. Fix: WAL mode + busy_timeout=10000 + SetMaxOpenConns(1) on writer + `BEGIN IMMEDIATE` + background WAL checkpoint goroutine — all wired in Phase 1, non-negotiable.

2. **Data loss on unclean shutdown** (C-5): Fly.io machine stops kill the process; the in-memory ingest channel is lost. Without a SIGTERM handler that drains the channel to SQLite within a bounded timeout, every deploy loses recent trace events. Fix: `signal.NotifyContext` from `main()`, 10s drain timeout, `db.Close()` after drain. Must be in Phase 1 architecture.

3. **CGo breaks the static binary promise** (C-4): `mattn/go-sqlite3` silently compiles with a CGo stub when `CGO_ENABLED=0` and panics at runtime. Fix: `modernc.org/sqlite` from day one. CI must verify `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` passes on every PR.

4. **Container CPU/memory metrics report host values, not cgroup quota** (M-1, M-6): `/proc/stat` reports host-wide CPU across all cores; `/proc/meminfo` reports host RAM. A Fly.io container with a 1-core quota looks like it uses 0.3% CPU on a 16-core host. First CPU sample always reads ~100% because there is no prior delta. Fix: read cgroup files (`/sys/fs/cgroup/cpu.max`, `/sys/fs/cgroup/memory.current`) for container-scoped values; discard first CPU sample.

5. **VACUUM + duplicate push events cause data integrity failures** (C-3, M-5): VACUUM requires 2x DB size in free disk space and will fail or kill the machine on a 1GB volume. Retry-after-timeout pushes the same batch twice, doubling cost metrics in the remote dashboard. Fix: use `PRAGMA auto_vacuum=INCREMENTAL` (never run VACUUM); include `trace_id` in every push payload and mark `pushed_at` only on confirmed HTTP 200 — never on network error.

## Build Order Recommendation

**Phase 1 — Foundation and ingest path (get data flowing, v0.1)**
1. Config struct via `sethvargo/go-envconfig` (all env vars with defaults)
2. SQLite init: open DB, apply all pragmas (WAL, busy_timeout, auto_vacuum, cache_size), create tables with `trace_id` column, separate writer/reader `*sql.DB` pools
3. Event type definitions (`LLMTrace`, `SystemMetric`, `CustomEvent`, `ErrorEvent`)
4. Persist Worker goroutine (single writer, batch insert, `BEGIN IMMEDIATE`)
5. HTTP server with all four timeouts + `MaxBytesReader`; ingest handlers + `/health`
6. Signal handling: `signal.NotifyContext`, graceful drain with 10s timeout, `db.Close()`
7. `GOMEMLIMIT` set in Dockerfile/start.sh at 80% of container RAM

**Phase 2 — System metrics and data management (v0.2)**
8. System Collector: CPU (cgroup-aware, discard first sample), RAM (cgroup), disk (`/data`), network, process count; push into ingest channel
9. Volume-aware SQLite cleanup: size check, DELETE old synced rows, `incremental_vacuum`, WAL checkpoint background goroutine
10. pprof/expvar self-observability (two blank imports + custom counters)
11. Process monitoring and zombie detection

**Phase 3 — Remote push pipeline (v0.3)**
12. Batch Reader: SELECT unsynced rows across all 4 tables, assemble JSON payload, enforce 5MB size cap with splitting
13. HTTP push client: custom `http.Client{Timeout: 30s}`, exponential backoff + full jitter, circuit breaker after 3 consecutive failed cycles
14. Push Scheduler: ticker, in-flight mutex guard, final flush on shutdown, mark `pushed_at` only on HTTP 200

**Phase 4 — Hardening (v1.0)**
15. Push resilience: idempotency tokens, `last_push_cursor`, `Retry-After` header handling
16. Integration tests: real SQLite + `httptest.NewServer` mock push target
17. Resource profiling: verify RSS <20MB, binary <15MB, binary size in CI
18. Optional: OpenClaw log parsing enrichment mode

## Key Numbers

| Metric | Target | Research-Backed Estimate |
|---|---|---|
| Binary size (stripped, `-s -w -trimpath`) | <15MB | 8-10MB; gopsutil + modernc/sqlite dominate |
| RSS at idle | <20MB | ~13MB: Go runtime 4MB + SQLite page cache 4MB + gopsutil 1MB + channels/goroutines 1MB + push buffer 1MB + HTTP 2MB |
| RSS during push (brief spike) | <30MB | 18-25MB: JSON serialization buffer adds ~5-10MB transiently |
| CPU at idle | <1% | <0.1%: metrics ticker sleeps 15s, push ticker sleeps 5min |
| CPU during push cycle | brief spike | ~0.5% spike, <500ms; I/O-bound, not CPU-bound |
| SQLite DB steady state | <50MB ceiling | 20-50MB for 7-day retention at moderate LLM call volume |
| WAL file | <10MB normal | Capped by background checkpoint goroutine; alert at 10MB, force RESTART at 50MB |
| Disk footprint on `/data` | <70MB total | DB 20-50MB + WAL 1-5MB + binary 10MB = ~35-70MB / 1GB volume |
| Goroutines at idle | ~8-9 | HTTP pool + Persist Worker + System Collector + Push Scheduler + Signal Handler |
| modernc/sqlite write speed | sufficient | ~2x slower than CGo driver; mitigated by batch inserts (50 events or 500ms) |

## Open Questions

- **Signal propagation from start.sh**: The sidecar runs as a background child of `start.sh` (`openclaw-trace &`). Fly.io sends SIGTERM to PID 1 (start.sh); signal propagation to background children depends on shell behavior. Needs explicit testing — may require `kill $TRACER_PID` in start.sh's own trap handler or a process supervisor.
- **Remote API idempotency contract**: The push design requires the `augmi.world /api/traces/ingest` endpoint to deduplicate on `trace_id`. This endpoint does not exist yet. The receiver schema and idempotency behavior must be designed alongside the sidecar, not after.
- **Text truncation policy**: LLM input/output text can be very large (100KB+ for long context windows). Storing full text in SQLite risks hitting the 8MiB/s Fly.io volume write limit and bloating the DB rapidly. A configurable `TRACE_MAX_TEXT_BYTES` truncation limit is needed but the right default (4KB? 16KB?) is unresolved.
- **gopsutil vs prometheus/procfs for cgroup-aware metrics**: PITFALLS.md recommends `prometheus/procfs` for cgroup v1/v2 detection; STACK.md recommends `gopsutil/v4`. These are not mutually exclusive but using both adds binary weight. Needs a decision: does gopsutil/v4's cgroup support cover the container-scoped CPU/memory requirements, or is a separate procfs read required?
- **Port 9090 conflict**: Port 9090 is Prometheus default and may conflict on some Fly.io machine configurations. The port must be configurable via `TRACE_PORT` but the conflict risk on the OpenClaw machines specifically has not been verified.
- **OTLP compatibility**: Deferred to v2. When multiple heterogeneous producers need to send traces, OTLP HTTP compatibility becomes a significant interoperability win. Field names should be aligned with OpenTelemetry GenAI semantic conventions from v1 to make the migration non-breaking.

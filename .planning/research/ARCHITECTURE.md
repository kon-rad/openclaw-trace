# Architecture Research: openclaw-trace

## Reference Architectures

### Sidecar Pattern Precedents

**Datadog Agent (Go rewrite, Agent v6+)**
The seminal reference for a lightweight Go monitoring sidecar. Key lessons:
- Monolithic single process replaced three legacy processes (Forwarder, Collector, DogStatsD)
- Uses a component framework with dependency injection (Uber FX) to decouple logical units
- Pipeline architecture: collect → aggregate/demultiplex → forward
- Components communicate via internal channels, not shared globals
- Specialized sub-agents handle security, OTel, etc. as separate processes when needed

**Grafana Alloy (successor to Grafana Agent)**
Component-based pipeline where each unit has defined inputs and outputs. Data flows through wired components. Supports pull (scrape) and push (receive) ingestion modes. Relevant for: the "wiring" mental model where each component exposes a typed interface.

**Vector (by Datadog)**
Source → Transform → Sink architecture. Each stage is a typed unit. Internal buffering between stages with configurable strategies (memory vs disk). Relevant for: the source/sink separation and explicit buffer contracts between stages.

**Fluent Bit**
Input plugins → Filter plugins → Output plugins, connected by an internal messaging bus. Extremely low memory footprint (target: ~450KB RSS). Relevant for: achieving minimal resource footprint through careful allocation and C-style buffer management, adapted here for Go.

**OpenTelemetry Collector**
Receiver → Processor → Exporter pipeline. Receivers accept push (HTTP/gRPC) and pull (scrape) data. Processors transform/filter. Exporters batch and ship. Key pattern: the Collector runs as a sidecar receiving OTLP events from the application via localhost, then batching/exporting to a backend. This is the closest direct analogue to openclaw-trace's LLM event ingestion path.

### Key Pattern for openclaw-trace
The closest model is **OTel Collector sidecar** for the ingestion path + **Datadog Agent** for the system metrics path, simplified to:
- A single Go binary
- No plugin system (fixed receivers/exporters)
- SQLite instead of in-memory aggregation only
- Minimal dependencies (stdlib + modernc SQLite)

---

## Proposed Component Design

```
┌─────────────────────────────────────────────────────────────────────┐
│                         openclaw-trace binary                       │
│                                                                     │
│  ┌──────────────┐   ┌──────────────────────────────────────────┐   │
│  │  HTTP Server │   │              Event Router                 │   │
│  │  :9090       │──▶│  /v1/traces  →  LLM Trace Handler        │   │
│  │              │   │  /v1/events  →  Custom Event Handler      │   │
│  │  /health     │   │  /v1/errors  →  Error Event Handler       │   │
│  └──────────────┘   └─────────────────────┬────────────────────┘   │
│                                           │                         │
│                                     Ingest Queue                    │
│                                    (buffered chan)                   │
│                                           │                         │
│  ┌──────────────┐                   ┌─────▼──────┐                 │
│  │  System      │                   │  Persist   │                 │
│  │  Collector   │──────────────────▶│  Worker    │                 │
│  │  (ticker)    │   system events   │  (SQLite)  │                 │
│  └──────────────┘                   └─────┬──────┘                 │
│                                           │                         │
│                                    SQLite Database                  │
│                                    /data/trace.db                   │
│                                           │                         │
│  ┌──────────────┐                   ┌─────▼──────┐                 │
│  │  Push        │◀──────────────────│  Batch     │                 │
│  │  Scheduler   │   trigger push    │  Reader    │                 │
│  │  (ticker)    │                   └────────────┘                 │
│  └──────┬───────┘                                                   │
│         │                                                           │
│  ┌──────▼───────┐                                                   │
│  │  HTTP Client │                                                   │
│  │  + Retry     │──▶ Remote API (augmi.world or custom endpoint)   │
│  └──────────────┘                                                   │
│                                                                     │
│  ┌──────────────┐                                                   │
│  │  Log Parser  │──▶ Ingest Queue (optional, log tail mode)        │
│  │  (optional)  │                                                   │
│  └──────────────┘                                                   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Component Descriptions

**HTTP Server**
- Listens on port 9090 (configurable via `TRACE_PORT`)
- Single-purpose: accept incoming events from the agent and health checks
- Returns `202 Accepted` immediately; never blocks on DB write
- Routes to typed handlers per endpoint path
- Exposes `/health` returning `{"status":"ok","db":"ok","queue_depth":N}`

**Event Router**
- Thin layer validating and typing incoming JSON payloads
- Dispatches validated events onto the ingest queue (buffered channel)
- Drops events when queue is full (with counter increment); returns `202` regardless
- Three event types: `LLMTrace`, `CustomEvent`, `ErrorEvent`

**Ingest Queue**
- A single `chan Event` with a fixed capacity (default: 512 events)
- Decouples the HTTP handler goroutines from the SQLite writer
- Backpressure: if full, the router drops and increments a `dropped_events` counter exposed on `/health`
- One goroutine drains this channel (the Persist Worker)

**Persist Worker**
- Single goroutine owns all SQLite writes (single-writer pattern)
- Reads from the ingest queue in a tight loop
- Batches writes using `BEGIN TRANSACTION` over N events or a time window (whichever comes first)
- Handles DB rotation: checks file size every N writes, triggers cleanup if above threshold

**System Collector**
- Runs on a configurable ticker (default: 15s)
- Uses `github.com/shirou/gopsutil/v3` for `/proc` abstractions (CPU, RAM, disk, network, processes)
- Constructs `SystemMetric` events and pushes them onto the ingest queue
- Skips a collection cycle if the ingest queue is near capacity (prevents metrics from starving trace events)

**Batch Reader**
- Called by the Push Scheduler; reads the oldest N unsynced rows from SQLite
- Returns a `[]BatchRow` slice for the push pipeline
- Marks rows as `synced=true` after a successful push (or records retry count)
- Separate from the Persist Worker's write path — uses a read-only DB connection

**Push Scheduler**
- Runs on a configurable ticker (default: 5 minutes)
- Triggers a batch read + push cycle
- Also triggered on graceful shutdown to flush pending data
- Does not run concurrent push cycles (uses a mutex/flag to skip if a push is already in flight)

**HTTP Client + Retry**
- Wraps `net/http` with exponential backoff and full jitter
- Uses `hashicorp/go-retryablehttp` or an inline implementation (stdlib preferred for binary size)
- Max retries: 3, with backoff: 1s, 2s, 4s + jitter
- Timeout per attempt: 30s
- On permanent failure (exhausted retries), increments `push_failures` counter; data remains in SQLite for next cycle

**Log Parser (optional)**
- Activated via `TRACE_LOG_PARSE=true`
- Tails the OpenClaw gateway log file (path configurable)
- Parses structured JSON log lines for additional context (model used, request IDs)
- Enriches existing trace events in SQLite rather than creating new ones
- Runs as an independent goroutine with its own context

---

## Data Flow

### LLM Trace Event (primary path)

```
OpenClaw Agent
  │
  │  POST /v1/traces HTTP/1.1  (fire-and-forget, no response wait)
  │  Content-Type: application/json
  │  { "provider": "anthropic", "model": "claude-sonnet-4", ... }
  ▼
HTTP Handler (goroutine, from net/http pool)
  │  Decode JSON → validate required fields → type as LLMTrace
  │  Non-blocking send on ingest chan
  │  Return 202 immediately
  ▼
Ingest Queue (buffered channel, cap=512)
  ▼
Persist Worker (single goroutine)
  │  Accumulate batch (max 50 events or 500ms window)
  │  BEGIN TRANSACTION
  │  INSERT INTO llm_traces (...)
  │  COMMIT
  ▼
SQLite: llm_traces table (WAL mode)
  ▼
Push Scheduler (ticker goroutine, every 5min)
  │  SELECT * FROM llm_traces WHERE synced=false LIMIT 500
  ▼
HTTP Client
  │  POST https://augmi.world/api/traces/ingest
  │  { "agent_id": "...", "traces": [...], "metrics": [...] }
  │  Retry with backoff on 5xx or network errors
  ▼
Remote API
  │  200 OK
  ▼
Batch Reader marks rows synced=true
```

### System Metrics Path

```
System Collector (ticker, every 15s)
  │  gopsutil: cpu.Percent, mem.VirtualMemory, disk.Usage, net.IOCounters
  │  Construct SystemMetric event
  │  Non-blocking send on ingest chan
  ▼
Ingest Queue (shared with LLM traces)
  ▼
Persist Worker
  │  INSERT INTO system_metrics (ts, cpu_pct, ram_mb, disk_pct, ...)
  ▼
SQLite: system_metrics table
  ▼
(same push path as LLM traces)
```

### Graceful Shutdown Flow

```
SIGTERM / SIGINT received
  ▼
Root context cancelled (context.WithCancel)
  │
  ├── HTTP Server: Shutdown(ctx with 10s timeout) — drains in-flight requests
  ├── System Collector: select on ctx.Done() — stops ticker
  ├── Log Parser: select on ctx.Done() — closes file handle
  │
  └── Ingest Queue: close(ingestChan) after above goroutines exit
        ▼
      Persist Worker: drains remaining events, commits final batch, closes DB
        ▼
      Push Scheduler: triggered once for final flush, then exits
        ▼
      sync.WaitGroup.Wait() — main() returns
```

---

## Concurrency Model

### Goroutine Map

| Goroutine | Count | Role |
|-----------|-------|------|
| `net/http` server pool | Variable (managed by Go runtime) | Handle incoming HTTP requests |
| Persist Worker | 1 | Single writer to SQLite |
| System Collector | 1 | Periodic `/proc` polling |
| Push Scheduler | 1 | Periodic batch push trigger |
| Push Executor | 0-1 (guarded by mutex) | Actual HTTP push to remote API |
| Log Parser | 0-1 (optional, configurable) | Tail gateway log file |
| Signal Handler | 1 | Listen for SIGTERM/SIGINT |

Total goroutines at idle: ~7-9 (well within Go runtime efficiency range)

### Channel Architecture

```go
// Central ingest queue — single producer group, single consumer
ingestChan chan Event  // cap: 512 (configurable via TRACE_QUEUE_SIZE)

// Shutdown coordination
rootCtx, rootCancel = context.WithCancel(context.Background())

// Push cycle guard
var pushMu sync.Mutex
var pushInFlight bool
```

### Key Invariants

1. **Single SQLite writer**: Only the Persist Worker goroutine writes to SQLite. No mutex needed for writes. The DB connection pool is configured with `MaxOpenConns(1)` for the write connection.
2. **Separate read connection**: The Batch Reader uses a distinct `*sql.DB` with WAL mode, allowing concurrent reads without blocking the Persist Worker.
3. **Non-blocking ingestion**: HTTP handlers never block. If the ingest queue is full, the event is dropped and a counter is incremented. The system degrades gracefully under load.
4. **Push non-reentrancy**: The Push Executor acquires a mutex before starting. If a push is already in flight when the scheduler fires, the scheduler skips the cycle.

### Ticker Pattern (idiomatic Go)

```go
func (sc *SystemCollector) Run(ctx context.Context) {
    ticker := time.NewTicker(sc.interval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            sc.collect(ctx)
        case <-ctx.Done():
            return
        }
    }
}
```

This pattern is used identically for System Collector, Push Scheduler, and the optional DB rotation check ticker.

---

## Storage Architecture

### SQLite Configuration

**Driver**: `modernc.org/sqlite` — pure Go, no CGo, no external `.so` dependencies. Binary-size penalty is ~5MB over a CGo driver but eliminates all deployment friction. Essential for the "single static binary" requirement.

**WAL Mode Setup (applied once at open time)**:
```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;   -- Safe with WAL; fsync on checkpoint only
PRAGMA temp_store = MEMORY;
PRAGMA cache_size = -4000;     -- 4MB page cache (negative = kilobytes)
PRAGMA busy_timeout = 5000;    -- 5s wait on lock contention
PRAGMA foreign_keys = ON;
PRAGMA mmap_size = 16777216;   -- 16MB memory-mapped I/O
```

**Connection Pool Strategy** (reader/writer separation):
```go
// Write connection — strictly single
writeDB, _ := sql.Open("sqlite", "file:/data/trace.db?_journal_mode=WAL&...")
writeDB.SetMaxOpenConns(1)
writeDB.SetMaxIdleConns(1)

// Read connection — for batch reads during push
readDB, _ := sql.Open("sqlite", "file:/data/trace.db?mode=ro&...")
readDB.SetMaxOpenConns(4)  // multiple readers are fine with WAL
```

### Schema

```sql
CREATE TABLE llm_traces (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          INTEGER NOT NULL,              -- Unix timestamp ms
    provider    TEXT NOT NULL,
    model       TEXT NOT NULL,
    input_text  TEXT,
    output_text TEXT,
    prompt_tokens    INTEGER,
    completion_tokens INTEGER,
    cost_usd    REAL,
    latency_ms  INTEGER,
    status      TEXT NOT NULL DEFAULT 'ok',   -- 'ok' | 'error' | 'timeout'
    error_msg   TEXT,
    synced      INTEGER NOT NULL DEFAULT 0,   -- 0 = pending, 1 = pushed
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE system_metrics (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          INTEGER NOT NULL,
    cpu_pct     REAL,
    ram_mb      REAL,
    ram_pct     REAL,
    disk_mb     REAL,
    disk_pct    REAL,
    net_rx_kb   REAL,
    net_tx_kb   REAL,
    proc_count  INTEGER,
    synced      INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE custom_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          INTEGER NOT NULL,
    name        TEXT NOT NULL,
    metadata    TEXT,                          -- JSON blob
    synced      INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE error_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          INTEGER NOT NULL,
    source      TEXT NOT NULL,                 -- 'llm' | 'process' | 'system'
    error_type  TEXT NOT NULL,                 -- 'rate_limit' | 'timeout' | 'oom' | ...
    message     TEXT NOT NULL,
    metadata    TEXT,
    synced      INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Indexes for push queries (unsynced rows, sorted by time)
CREATE INDEX idx_llm_traces_unsynced ON llm_traces (synced, ts);
CREATE INDEX idx_system_metrics_unsynced ON system_metrics (synced, ts);
CREATE INDEX idx_custom_events_unsynced ON custom_events (synced, ts);
CREATE INDEX idx_error_events_unsynced ON error_events (synced, ts);
```

### Rotation and Cleanup Strategy

**Target**: Keep SQLite database under 50MB (configurable via `TRACE_DB_MAX_MB`). The `/data` volume is 1GB; 50MB is a conservative ceiling.

**Rotation trigger**: Checked every 1000 writes (in the Persist Worker) or every 5 minutes (separate ticker). Uses `os.Stat()` on the DB file — no SQL query needed.

**Cleanup method**: Delete oldest synced rows, not full file rotation (avoids WAL checkpoint complexity):
```sql
-- Delete synced rows older than retention window (default: 7 days)
DELETE FROM llm_traces
WHERE synced = 1
  AND created_at < unixepoch() - 604800
  AND id IN (SELECT id FROM llm_traces ORDER BY id ASC LIMIT 10000);

-- After deletion, checkpoint WAL and reclaim space
PRAGMA wal_checkpoint(TRUNCATE);
PRAGMA auto_vacuum = INCREMENTAL;
PRAGMA incremental_vacuum(1000);
```

**Emergency cleanup**: If DB size still exceeds max after normal cleanup, delete oldest unsynced rows too (with a warning log entry). Data loss is acceptable over crashing the host machine.

**Retention config**:
```
TRACE_RETENTION_DAYS=7        # Rows older than N days are eligible for cleanup
TRACE_DB_MAX_MB=50            # Trigger cleanup at this size
TRACE_DB_CLEANUP_INTERVAL=5m  # How often to check size
```

---

## Push Pipeline

### Batch Composition

On each push cycle, the Batch Reader runs one query per table:
```sql
SELECT * FROM llm_traces WHERE synced=0 ORDER BY ts ASC LIMIT 500;
SELECT * FROM system_metrics WHERE synced=0 ORDER BY ts ASC LIMIT 500;
SELECT * FROM custom_events WHERE synced=0 ORDER BY ts ASC LIMIT 500;
SELECT * FROM error_events WHERE synced=0 ORDER BY ts ASC LIMIT 500;
```

These are assembled into a single JSON payload:
```json
{
  "agent_id": "machine-abc123",
  "pushed_at": "2026-02-23T12:00:00Z",
  "traces": [...],
  "metrics": [...],
  "events": [...],
  "errors": [...]
}
```

Payload size is capped at 5MB (configurable). If the batch would exceed this, it is split across multiple sequential push calls.

### Retry and Backoff

**Algorithm**: Exponential backoff with full jitter (AWS-recommended for avoiding thundering herd):
```
delay = random(0, min(cap, base * 2^attempt))
```

**Parameters**:
- Base delay: 1s
- Cap: 60s
- Max attempts: 3 (per push cycle; next cycle is another opportunity)
- Jitter: full (randomize entire delay range)
- Retryable status codes: 429, 500, 502, 503, 504
- Non-retryable: 400, 401, 403 (bad payload or auth — do not retry, log error)

**Implementation**: Inline retry loop using `net/http` + `time.Sleep`. No external retry library to keep binary lean. The entire retry logic is ~40 lines.

### Post-Push Marking

On successful push (HTTP 200):
```sql
UPDATE llm_traces SET synced=1 WHERE id IN (?);
```
One UPDATE per table per successful batch. Done in a transaction for atomicity.

On failure after all retries: rows remain `synced=0`, will be included in next push cycle. A `push_failure_count` field is NOT added to the schema (keeps it simple); instead, rows accumulate until the next successful cycle.

### Circuit Breaker (simple)

After 3 consecutive failed push cycles (tracked in memory), the Push Scheduler backs off to 30-minute intervals instead of 5-minute intervals. This prevents hammering an unavailable remote API. Resets on first successful push.

---

## Build Order

Dependencies flow from bottom to top. Build in this sequence:

### Phase 1: Foundation
1. **Config** — `config/config.go`: Parse env vars + config file into a typed `Config` struct. No external deps. Test with unit tests.
2. **Schema + DB Init** — `storage/schema.go`: Open SQLite, apply pragmas, run `CREATE TABLE IF NOT EXISTS` migrations. Validates the modernc.org/sqlite integration end-to-end.
3. **Event Types** — `events/types.go`: Define `LLMTrace`, `SystemMetric`, `CustomEvent`, `ErrorEvent` structs with JSON tags. Pure data structures, no deps.

### Phase 2: Ingest Path
4. **Persist Worker** — `storage/worker.go`: Single goroutine draining a channel into SQLite. Batch insert logic. Test with a mock ingest channel.
5. **HTTP Server + Handlers** — `api/server.go`, `api/handlers.go`: Receive events, validate, push to ingest channel. Return 202. Test with `httptest`.
6. **Health Endpoint** — `api/health.go`: Query DB for basic sanity, expose queue depth. Needed for Fly.io health checks on port 9090.

### Phase 3: System Collection
7. **System Collector** — `collector/system.go`: Integrate gopsutil v3. CPU, RAM, disk, network, process list. Push system metrics events to ingest channel. Test on Linux with actual `/proc`.
8. **Log Parser** (optional) — `collector/logparser.go`: Tail a file, parse JSON lines, enrich traces. Build after system collector to reuse the same goroutine lifecycle pattern.

### Phase 4: Push Pipeline
9. **Batch Reader** — `storage/reader.go`: Read unsynced rows from all tables. Build the JSON payload. Handle size limits and splitting.
10. **HTTP Client + Retry** — `push/client.go`: Wrap `net/http`, implement exponential backoff + jitter, handle 4xx vs 5xx distinction.
11. **Push Scheduler** — `push/scheduler.go`: Ticker-driven push cycles, in-flight guard, circuit breaker logic, final flush on shutdown.

### Phase 5: Lifecycle
12. **Graceful Shutdown** — `main.go`: Wire all goroutines to a root context. Handle SIGTERM/SIGINT via `signal.NotifyContext`. `sync.WaitGroup` for clean exit. Flush pending data.
13. **DB Rotation** — `storage/cleanup.go`: Size check, DELETE old rows, WAL checkpoint. Integrate into the Push Scheduler ticker or its own ticker.

### Phase 6: Hardening
14. **Integration tests**: Start the binary, fire events at it, verify SQLite contents, simulate a push target with `httptest.NewServer`.
15. **Resource profiling**: Use `runtime.MemStats` + `pprof` to verify <20MB RSS. Tune batch sizes, cache settings.
16. **Binary size optimization**: Build with `-ldflags="-s -w"`, `GOFLAGS="-trimpath"`. Verify <15MB.

---

## Resource Budget

### Memory

| Component | Estimated RSS |
|-----------|---------------|
| Go runtime baseline | ~4MB |
| `net/http` server + goroutine pool | ~2MB |
| SQLite page cache (4MB config) | 4MB |
| Ingest queue (512 × ~1KB avg event) | ~0.5MB |
| gopsutil snapshot buffers | ~1MB |
| Push batch buffer (500 rows × ~2KB) | ~1MB |
| Stack space for ~8 goroutines | ~0.5MB |
| **Total estimate** | **~13MB** |

This provides ~7MB headroom under the 20MB target. The largest variable is the SQLite page cache; reduce `PRAGMA cache_size` to `-2000` (2MB) if running on constrained machines.

### CPU

| Activity | CPU Impact |
|----------|------------|
| Idle HTTP server | <0.01% |
| System metric collection (every 15s) | ~0.1% spike, <1ms |
| SQLite batch write (50 rows) | ~0.05% spike, <5ms |
| Push cycle (every 5min) | ~0.5% spike, <500ms for network I/O |
| **Idle average** | **<0.1%** |

CPU spikes during push are acceptable per requirements. The push HTTP call is I/O-bound (network), not CPU-bound.

### Disk

| Item | Size |
|------|------|
| Binary | <15MB (target) |
| SQLite DB (steady state, 7-day retention) | ~20-50MB |
| SQLite WAL file (between checkpoints) | ~1-5MB transient |
| Total volume impact | ~35-70MB / 1GB volume |

### Binary Size Strategy

- Use `modernc.org/sqlite` (pure Go, ~5MB addition to binary)
- Use `gopsutil/v3` selectively — import only the sub-packages needed (`cpu`, `mem`, `disk`, `net`, `process`), not the entire module
- Build with: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath`
- Avoid importing `encoding/xml`, `image/*`, and other large stdlib packages
- Use `go tool nm` to audit symbol bloat if binary exceeds 15MB

---

## Key Design Decisions Summary

| Decision | Rationale |
|----------|-----------|
| Single ingest channel (not per-type) | Simplicity; all types go to the same Persist Worker |
| Drop events when queue full | Fire-and-forget requirement; agent must never block |
| Single SQLite writer goroutine | Eliminates lock contention; simpler than connection pool guards |
| WAL mode + NORMAL sync | Best throughput for write-heavy workload with safe crash recovery |
| Soft delete (synced flag) vs DELETE | Simpler push logic; cleanup is a separate concern |
| Inline retry (no library) | Keeps binary small; retry logic is straightforward |
| gopsutil over raw /proc parsing | Handles edge cases, cross-platform dev on macOS, actively maintained |
| modernc.org/sqlite over mattn/go-sqlite3 | No CGo = truly static binary, simpler cross-compilation |

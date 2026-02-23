# Phase 1: Foundation - Research

**Researched:** 2026-02-23
**Domain:** Go binary structure, SQLite init, env config, slog, graceful shutdown, ServeMux, health endpoint
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Configuration naming & defaults**
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

**Health endpoint contract**
- Path: `GET /health` (not /healthz)
- Always returns HTTP 200 — health status conveyed via JSON `status` field
- Response fields (all four groups):
  - Core: `status` (ok/degraded/error), `uptime_seconds`, `version`
  - DB: `db_status` (ok/error), `db_size_bytes`, `wal_size_bytes`
  - Queue: `queue_depth` (ingest channel), `events_received`, `events_dropped`
  - Push: `last_push_time`, `last_push_status`, `unsynced_count`

**Startup & shutdown logging**
- Startup prints (in order): banner with version, config summary, DB init status, listening message
- Shutdown prints: signal received, drain progress, push flush result, final stats
- Log format: JSON always (one JSON object per line). No human-readable mode.

**SQLite table schema**
- trace_id format: UUID v4 string (36-char)
- Timestamps: Unix milliseconds (INTEGER) — fast sorting, compact
- LLM text storage: Inline TEXT columns (input_text, output_text) in llm_traces table
- Metadata: Every data table has a `metadata` TEXT column for arbitrary JSON
- Sync tracking: Two columns on every data table: `synced` BOOLEAN + `pushed_at` INTEGER (Unix ms)
- Indexes: Composite index on `(synced, created_at)` per data table
- Retention: Default 3 days (OCT_RETENTION_DAYS)
- Four tables: llm_traces, error_events, system_metrics, push_log

### Claude's Discretion
- Exact Go package layout (cmd/, internal/, etc.)
- SQLite cache_size pragma value
- Exact health check JSON field names (camelCase vs snake_case)
- Ingest channel buffer size tuning
- Signal handling implementation details
- How --help formats the env var listing

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| FOUN-01 | Single static Go binary with zero external dependencies (CGO_ENABLED=0) | modernc.org/sqlite (pure Go), CGO_ENABLED=0 build verified, binary size patterns documented |
| FOUN-02 | Configuration via environment variables with sane defaults (port, push interval, push endpoint, db path, log level) | sethvargo/go-envconfig v1 struct tag patterns, `required` / `default` tags, `Process(ctx, &cfg)` |
| FOUN-03 | Graceful shutdown: drain ingest channel, attempt final push, checkpoint WAL, close DB on SIGTERM | signal.NotifyContext pattern, http.Server.Shutdown, sync.WaitGroup coordination |
| FOUN-04 | Health check endpoint (GET /health) returning db status, queue depth, uptime, last push time | Go 1.22+ ServeMux `GET /health` handler, JSON response construction, os.Stat for db size |
| FOUN-05 | Structured JSON logging via slog with configurable log level | slog.NewJSONHandler, slog.LevelVar, LevelVar.UnmarshalText for string level parsing |
| STOR-01 | SQLite database at configurable path (default /data/openclaw-trace.db) with WAL mode | modernc.org/sqlite, RegisterConnectionHook pragma init, WAL mode setup |
| STOR-02 | Four tables: llm_traces, error_events, system_metrics, push_log | Schema DDL with UUID trace_id, Unix ms timestamps, metadata JSON column, synced + pushed_at columns |
| STOR-05 | SQLite pragmas: WAL mode, busy_timeout=10000, auto_vacuum=INCREMENTAL, appropriate cache_size | RegisterConnectionHook pattern, PRAGMA busy_timeout, PRAGMA auto_vacuum, PRAGMA synchronous=NORMAL |
</phase_requirements>

---

## Summary

Phase 1 builds the complete structural skeleton of openclaw-trace: a static Go binary that configures itself from environment variables, opens SQLite with correct WAL-mode pragmas, creates four tables, exposes `GET /health`, logs in structured JSON, and shuts down cleanly on SIGTERM. No ingest endpoints, no metrics collection, no push pipeline — those are Phases 2-4. Everything in this phase must be correct by design because later phases build on it and rearchitecting later is costly.

The research confirms that all Phase 1 technology decisions have HIGH confidence. `sethvargo/go-envconfig` v1 uses struct tags with `env:"OCT_PORT,default=9090"` syntax and a `Process(ctx, &cfg)` call — straightforward to implement. `modernc.org/sqlite` applies pragmas via `RegisterConnectionHook` which runs on every new connection — the right hook for ensuring WAL mode and busy_timeout are always set. `log/slog` with `slog.LevelVar` supports runtime-dynamic log levels and parses level strings via `LevelVar.UnmarshalText`. `signal.NotifyContext` from Go 1.16+ handles SIGTERM cleanly and internally uses a buffered channel — the canonical pattern for graceful shutdown. Go 1.22+ `net/http.ServeMux` supports `"GET /health"` method-prefixed patterns natively, no router needed.

**Primary recommendation:** Build the foundation in strict dependency order: Config → SQLite init → Schema DDL → slog setup → HTTP server + health → signal handling. Each component must be independently testable. Do not conflate the layers. Get signal handling wired to `main()` from the very first commit — retrofitting it later risks data loss patterns in production.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go | 1.26 | Language runtime | Current stable; Green Tea GC default reduces memory overhead; CGO_ENABLED=0 static builds |
| modernc.org/sqlite | v1.46.1 | SQLite driver (pure Go) | Only CGo-free SQLite driver; cross-compiles cleanly; 2,562+ importers on pkg.go.dev |
| github.com/sethvargo/go-envconfig | v1.3.0 | Env var config parsing | Zero transitive deps; struct-tag based; maintained by Seth Vargo; `required`/`default` tags |
| log/slog (stdlib) | Go 1.21+ | Structured JSON logging | Standard library; JSON handler; LevelVar for dynamic levels; zero dep cost |
| net/http (stdlib) | Go 1.22+ | HTTP server + ServeMux | Method-prefixed routing `"GET /health"` natively; no router framework needed |
| github.com/google/uuid | v1.x | UUID v4 generation | De facto standard; `uuid.New()` produces v4 UUID as `[16]byte`; `.String()` gives 36-char form |
| os/signal (stdlib) | Go 1.16+ | Signal handling | `signal.NotifyContext` handles SIGTERM/SIGINT; internally buffered; canonical pattern |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| flag (stdlib) | Go builtin | --help and --version flags | Define `flag.Bool("version", ...)`, customize `flag.Usage` to list all OCT_ env vars |
| sync (stdlib) | Go builtin | WaitGroup for goroutine drain | `sync.WaitGroup` to wait for all goroutines to finish before `db.Close()` |
| context (stdlib) | Go builtin | Cancellation propagation | Root context from `signal.NotifyContext` flows to all goroutines |
| database/sql (stdlib) | Go builtin | SQL abstraction layer | Two `*sql.DB` instances: writer (`MaxOpenConns(1)`) and reader |
| os (stdlib) | Go builtin | File size checks for health | `os.Stat(dbPath)` for `db_size_bytes`; `os.Stat(dbPath+"-wal")` for `wal_size_bytes` |
| time (stdlib) | Go builtin | Uptime tracking, push interval | `startTime := time.Now()` at startup; `time.Since(startTime).Seconds()` for uptime |
| encoding/json (stdlib) | Go builtin | Health response serialization | `json.NewEncoder(w).Encode(response)` for health endpoint |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| sethvargo/go-envconfig | kelseyhightower/envconfig | kelseyhightower is unmaintained, lacks context support; sethvargo is the modern successor |
| sethvargo/go-envconfig | github.com/spf13/viper | Viper is massively over-engineered for env-only config; adds ~3MB of transitive deps |
| modernc.org/sqlite | mattn/go-sqlite3 | mattn requires CGo — breaks static builds, breaks cross-compilation; rejected |
| log/slog | zerolog or zap | Zerolog/zap are faster but sidecar log volume is low; stdlib dependency is worth it |
| net/http ServeMux | chi, gin, echo | All add binary weight for features the sidecar doesn't need; 5 fixed endpoints don't need a framework |
| google/uuid | github.com/rs/xid | xid is shorter but not UUID v4; trace_id is user-visible and UUID v4 is the expected format |

**Installation:**
```bash
go get github.com/sethvargo/go-envconfig@v1.3.0
go get modernc.org/sqlite@v1.46.1
go get github.com/google/uuid@latest
```

---

## Architecture Patterns

### Recommended Project Structure

```
openclaw-trace/
├── cmd/
│   └── openclaw-trace/
│       └── main.go          # Entry point: wire all components, signal handling
├── internal/
│   ├── config/
│   │   └── config.go        # Config struct with env tags, Process() call, --help/--version
│   ├── db/
│   │   ├── db.go            # Open SQLite, apply RegisterConnectionHook, return writer+reader *sql.DB
│   │   └── schema.go        # CREATE TABLE IF NOT EXISTS DDL for all 4 tables + indexes
│   ├── server/
│   │   ├── server.go        # http.Server construction with all timeouts
│   │   └── health.go        # GET /health handler — reads atomic counters + db stats
│   └── logging/
│       └── logging.go       # slog JSON handler setup, LevelVar, parseLevel()
├── go.mod
└── go.sum
```

**Rationale for this layout:**
- Single binary → single `cmd/openclaw-trace/` entry point (no `cmd/` proliferation)
- `internal/` enforces that these packages are not importable by external code
- Each subdirectory has one clear responsibility
- Phase 2-4 additions (ingest, collector, push) get their own `internal/` packages without touching Phase 1 code
- Flat over deep — avoid `internal/storage/sqlite/db.go` nesting for a small binary

### Pattern 1: Configuration with sethvargo/go-envconfig

**What:** Struct tags drive all env var parsing. A single `envconfig.Process(ctx, &cfg)` call populates the entire config. `required` tag errors immediately if the var is unset. `default` tag provides fallbacks. The `OCT_` prefix is embedded in each tag — go-envconfig does not have a global prefix option.

**When to use:** At the very start of `main()`, before any other initialization.

```go
// Source: pkg.go.dev/github.com/sethvargo/go-envconfig
package config

import (
    "context"
    "fmt"
    "time"

    "github.com/sethvargo/go-envconfig"
)

type Config struct {
    Port           string        `env:"OCT_PORT,default=9090"`
    DBPath         string        `env:"OCT_DB_PATH,default=/data/openclaw-trace.db"`
    LogLevel       string        `env:"OCT_LOG_LEVEL,default=info"`
    PushEndpoint   string        `env:"OCT_PUSH_ENDPOINT"`              // empty = push disabled
    PushInterval   time.Duration `env:"OCT_PUSH_INTERVAL,default=5m"`
    RetentionDays  int           `env:"OCT_RETENTION_DAYS,default=3"`
    MaxTextBytes   int           `env:"OCT_MAX_TEXT_BYTES,default=16384"`
}

func Load(ctx context.Context) (*Config, error) {
    var cfg Config
    if err := envconfig.Process(ctx, &cfg); err != nil {
        return nil, fmt.Errorf("config: %w", err)
    }
    return &cfg, nil
}
```

**Important:** A field cannot be both `required` and have a `default`. `OCT_PUSH_ENDPOINT` is intentionally neither — empty string means push is disabled, which is logged as a warning but does not fail startup.

### Pattern 2: slog JSON Handler with Dynamic Level

**What:** Create a `slog.LevelVar` at startup, parse the `OCT_LOG_LEVEL` string into it, pass it to `slog.NewJSONHandler`. The `LevelVar` can be changed at runtime. `LevelVar.UnmarshalText([]byte("debug"))` is the canonical way to parse level strings.

```go
// Source: pkg.go.dev/log/slog
package logging

import (
    "fmt"
    "log/slog"
    "os"
    "strings"
)

// LevelVar allows runtime log level changes (e.g., via /debug endpoint in future)
var Level = new(slog.LevelVar)

func Setup(levelStr string) (*slog.Logger, error) {
    levelStr = strings.ToLower(strings.TrimSpace(levelStr))
    if err := Level.UnmarshalText([]byte(levelStr)); err != nil {
        return nil, fmt.Errorf("invalid log level %q: %w", levelStr, err)
    }
    handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: Level,
    })
    logger := slog.New(handler)
    slog.SetDefault(logger)
    return logger, nil
}
```

**JSON output format** (one object per line, parseable by Fly.io log drains):
```json
{"time":"2026-02-23T12:00:00Z","level":"INFO","msg":"openclaw-trace v0.1.0 starting..."}
{"time":"2026-02-23T12:00:00Z","level":"INFO","msg":"SQLite opened","path":"/data/openclaw-trace.db","mode":"WAL"}
```

### Pattern 3: modernc.org/sqlite — RegisterConnectionHook for Pragma Init

**What:** `sqlite.RegisterConnectionHook` registers a function called on every new connection. This is the canonical way to ensure WAL mode and other pragmas are applied on every connection, not just the first. The hook runs before the connection is returned to the pool.

**Critical detail:** The hook is global — it applies to ALL connections opened via the `"sqlite"` driver. For a binary with only one SQLite database, this is perfectly fine. If you needed per-database hooks, you would use `sqlite.NewDriver()` with a dedicated driver instance.

```go
// Source: pkg.go.dev/modernc.org/sqlite + theitsolutions.io/blog/modernc.org-sqlite-with-go
package db

import (
    "context"
    "database/sql"
    "fmt"

    "modernc.org/sqlite"
    _ "modernc.org/sqlite"
)

const pragmaSQL = `
    PRAGMA journal_mode = WAL;
    PRAGMA synchronous = NORMAL;
    PRAGMA busy_timeout = 10000;
    PRAGMA temp_store = MEMORY;
    PRAGMA mmap_size = 67108864;
    PRAGMA auto_vacuum = INCREMENTAL;
    PRAGMA foreign_keys = ON;
    PRAGMA cache_size = -8000;
`

func init() {
    sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
        _, err := conn.ExecContext(context.Background(), pragmaSQL)
        return err
    })
}

// OpenDB returns a writer (MaxOpenConns=1) and a reader (*sql.DB).
// Caller must close both.
func OpenDB(path string) (writer *sql.DB, reader *sql.DB, err error) {
    dsn := "file:" + path

    writer, err = sql.Open("sqlite", dsn)
    if err != nil {
        return nil, nil, fmt.Errorf("open writer: %w", err)
    }
    writer.SetMaxOpenConns(1)
    writer.SetMaxIdleConns(1)
    writer.SetConnMaxLifetime(0) // keep connection alive

    reader, err = sql.Open("sqlite", dsn)
    if err != nil {
        writer.Close()
        return nil, nil, fmt.Errorf("open reader: %w", err)
    }
    reader.SetMaxOpenConns(4) // WAL mode allows concurrent readers

    // Ping to trigger connection and apply hook
    if err := writer.PingContext(context.Background()); err != nil {
        writer.Close()
        reader.Close()
        return nil, nil, fmt.Errorf("ping writer: %w", err)
    }
    return writer, reader, nil
}
```

**DSN alternative for pragmas:** DSN params like `?_pragma=journal_mode(WAL)` also work in modernc.org/sqlite. However, `RegisterConnectionHook` is recommended for clarity and to apply multi-statement blocks.

**The `_txlock=immediate` DSN param** can enforce `BEGIN IMMEDIATE` behavior at the connection level instead of manually writing `BEGIN IMMEDIATE` in transaction code. However, since the Persist Worker is the sole writer and uses a single connection, explicit `BEGIN IMMEDIATE` in write transactions is clearer.

### Pattern 4: Schema DDL — Four Tables with Correct Types

**What:** All four tables must be created with `CREATE TABLE IF NOT EXISTS` (idempotent), `trace_id TEXT` as the UUID column (not PRIMARY KEY — use INTEGER AUTOINCREMENT for SQLite efficiency), Unix millisecond timestamps as INTEGER, and `metadata TEXT` + `synced INTEGER` + `pushed_at INTEGER` on every data table.

**Important SQLite type affinity note:** SQLite has no BOOLEAN type; use `INTEGER NOT NULL DEFAULT 0` for `synced`. Use `INTEGER` for all timestamps (Unix ms). Use `TEXT` for UUIDs (36-char string).

```sql
-- Source: CONTEXT.md decisions + SQLite docs (sqlite.org/datatype3.html)

CREATE TABLE IF NOT EXISTS llm_traces (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id        TEXT    NOT NULL UNIQUE,           -- UUID v4, 36-char
    created_at      INTEGER NOT NULL,                  -- Unix ms
    provider        TEXT    NOT NULL,
    model           TEXT    NOT NULL,
    input_text      TEXT,                              -- truncated to OCT_MAX_TEXT_BYTES
    output_text     TEXT,                              -- truncated to OCT_MAX_TEXT_BYTES
    prompt_tokens   INTEGER,
    completion_tokens INTEGER,
    total_tokens    INTEGER,
    cost_usd        REAL,
    latency_ms      INTEGER,
    status          TEXT    NOT NULL DEFAULT 'ok',     -- 'ok' | 'error' | 'timeout'
    error_type      TEXT,
    metadata        TEXT,                              -- arbitrary JSON
    synced          INTEGER NOT NULL DEFAULT 0,        -- 0=pending, 1=pushed
    pushed_at       INTEGER                            -- Unix ms, NULL until pushed
);

CREATE TABLE IF NOT EXISTS error_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id        TEXT    NOT NULL UNIQUE,
    created_at      INTEGER NOT NULL,
    error_type      TEXT    NOT NULL,                  -- 'llm_error' | 'crash' | 'system_error'
    message         TEXT    NOT NULL,
    stack_trace     TEXT,
    severity        TEXT    NOT NULL DEFAULT 'error',  -- 'warning' | 'error' | 'critical'
    metadata        TEXT,
    synced          INTEGER NOT NULL DEFAULT 0,
    pushed_at       INTEGER
);

CREATE TABLE IF NOT EXISTS system_metrics (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id        TEXT    NOT NULL UNIQUE,
    created_at      INTEGER NOT NULL,
    cpu_pct         REAL,
    mem_rss_bytes   INTEGER,
    mem_available   INTEGER,
    mem_total       INTEGER,
    disk_used_bytes INTEGER,
    disk_total_bytes INTEGER,
    disk_free_bytes INTEGER,
    metadata        TEXT,
    synced          INTEGER NOT NULL DEFAULT 0,
    pushed_at       INTEGER
);

CREATE TABLE IF NOT EXISTS push_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at      INTEGER NOT NULL,                  -- Unix ms
    status          TEXT    NOT NULL,                  -- 'ok' | 'error'
    events_pushed   INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT,
    duration_ms     INTEGER
);

-- Composite indexes covering the primary push query: WHERE synced=0 ORDER BY created_at ASC
CREATE INDEX IF NOT EXISTS idx_llm_synced ON llm_traces (synced, created_at);
CREATE INDEX IF NOT EXISTS idx_errors_synced ON error_events (synced, created_at);
CREATE INDEX IF NOT EXISTS idx_metrics_synced ON system_metrics (synced, created_at);
```

**push_log does not need synced/pushed_at** — it is local audit data, never pushed to remote.

### Pattern 5: HTTP Server with All Timeouts

**What:** Never use `http.ListenAndServe` directly — always configure an `http.Server` struct with all four timeouts. Go 1.22+ ServeMux supports `"GET /health"` method-prefixed patterns.

```go
// Source: go.dev/blog/routing-enhancements + blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts
package server

import (
    "net/http"
    "time"
)

func New(addr string, h *HealthHandler) *http.Server {
    mux := http.NewServeMux()
    mux.HandleFunc("GET /health", h.Handle)
    // Phase 2+ will add: POST /v1/traces, POST /v1/errors
    // These register cleanly alongside GET /health

    return &http.Server{
        Addr:              addr,
        Handler:           mux,
        ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout:       10 * time.Second,
        WriteTimeout:      10 * time.Second,
        IdleTimeout:       60 * time.Second,
    }
}
```

**GET also matches HEAD** — per Go docs, `"GET /health"` pattern matches both GET and HEAD requests. This is correct behavior for a health endpoint.

**Method Not Allowed automatic response:** If a POST is sent to `/health`, Go 1.22+ ServeMux automatically returns `405 Method Not Allowed` with an `Allow: GET, HEAD` header. No manual method checking needed.

### Pattern 6: signal.NotifyContext Graceful Shutdown

**What:** `signal.NotifyContext` (Go 1.16+) returns a context that is cancelled when the specified signals arrive. It internally uses a buffered channel — avoiding the `N-3` pitfall of dropped signals. The returned `stop` function should be called after `ctx.Done()` to reset signal handling (so a second Ctrl+C hard-exits).

```go
// Source: victoriametrics.com/blog/go-graceful-shutdown + pkg.go.dev/os/signal
package main

import (
    "context"
    "os/signal"
    "sync"
    "syscall"
    "time"
)

func main() {
    // Root context — cancelled by SIGTERM or SIGINT
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    // WaitGroup tracks all goroutines that must finish before exit
    var wg sync.WaitGroup

    // Start components, passing ctx down
    // Each goroutine: select { case <-ctx.Done(): return }

    // Block until signal
    <-ctx.Done()
    stop() // allow second signal to hard-exit

    slog.Info("SIGTERM received, shutting down...")

    // Shutdown HTTP server (stop accepting new requests, drain in-flight)
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    srv.Shutdown(shutdownCtx)

    // Wait for all goroutines to finish (with overall timeout)
    doneCh := make(chan struct{})
    go func() { wg.Wait(); close(doneCh) }()
    select {
    case <-doneCh:
        slog.Info("all goroutines stopped")
    case <-time.After(15 * time.Second):
        slog.Warn("shutdown timeout — forcing exit")
    }

    // db.Close() here — after all goroutines are done
    // This triggers a final WAL checkpoint
    db.Close()
    slog.Info("shutdown complete")
}
```

**Order of shutdown operations:**
1. `ctx.Done()` received → cancel root context
2. HTTP server: `srv.Shutdown(10s ctx)` — drains in-flight requests
3. All goroutines: detect `ctx.Done()`, finish current work, exit
4. WaitGroup: blocks until all goroutines report done
5. `db.Close()` — triggers final WAL checkpoint, flushes page cache
6. Log final stats, return from main()

### Pattern 7: --help / --version with flag Package

**What:** Use stdlib `flag` package. Define `--version` as `flag.Bool`. Override `flag.Usage` to print all `OCT_` environment variables with their defaults. `-help` or `-h` is automatically handled by the flag package (returns `flag.ErrHelp`).

```go
// Source: pkg.go.dev/flag
package main

import (
    "flag"
    "fmt"
    "os"
)

var (
    version = "dev" // overridden by -ldflags "-X main.version=v0.1.0"
    commit  = "unknown"
)

func setupFlags() {
    showVersion := flag.Bool("version", false, "print version and exit")

    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "openclaw-trace %s\n\n", version)
        fmt.Fprintf(os.Stderr, "A lightweight observability sidecar for OpenClaw AI agents.\n\n")
        fmt.Fprintf(os.Stderr, "Environment variables:\n")
        fmt.Fprintf(os.Stderr, "  %-30s %s (default: %s)\n", "OCT_PORT", "HTTP listen port", "9090")
        fmt.Fprintf(os.Stderr, "  %-30s %s (default: %s)\n", "OCT_DB_PATH", "SQLite database path", "/data/openclaw-trace.db")
        fmt.Fprintf(os.Stderr, "  %-30s %s (default: %s)\n", "OCT_LOG_LEVEL", "Log level (debug/info/warn/error)", "info")
        fmt.Fprintf(os.Stderr, "  %-30s %s\n", "OCT_PUSH_ENDPOINT", "Remote push URL (empty = push disabled)")
        fmt.Fprintf(os.Stderr, "  %-30s %s (default: %s)\n", "OCT_PUSH_INTERVAL", "Push interval", "5m")
        fmt.Fprintf(os.Stderr, "  %-30s %s (default: %s)\n", "OCT_RETENTION_DAYS", "Data retention in days", "3")
        fmt.Fprintf(os.Stderr, "  %-30s %s (default: %s)\n", "OCT_MAX_TEXT_BYTES", "Max LLM text bytes stored", "16384")
        fmt.Fprintf(os.Stderr, "\nFlags:\n")
        flag.PrintDefaults()
    }

    flag.Parse()

    if *showVersion {
        fmt.Printf("openclaw-trace %s (commit: %s)\n", version, commit)
        os.Exit(0)
    }
}
```

**Key behavior:** `flag.Parse()` will call `flag.Usage` and exit with code 2 when `-h` or `--help` is passed, because those flags are not defined and the package returns `flag.ErrHelp`. This is standard Go behavior. Calling `flag.Bool("help", ...)` explicitly is optional — the flag package handles `-h` automatically.

### Pattern 8: UUID v4 Generation

**What:** `github.com/google/uuid` is the standard. `uuid.New()` is the convenience function that panics on error (acceptable at ingest time — crypto/rand failure is unrecoverable). `uuid.NewRandom()` returns an error for callers that handle errors explicitly.

```go
// Source: pkg.go.dev/github.com/google/uuid
import "github.com/google/uuid"

// At ingest time — generate trace_id
traceID := uuid.New().String() // "6ba7b810-9dad-11d1-80b4-00c04fd430c8" format (36 chars)

// For callers that want to handle error explicitly
id, err := uuid.NewRandom()
if err != nil {
    return fmt.Errorf("generate trace_id: %w", err)
}
traceID := id.String()
```

**Note:** Phase 1 does not need UUID generation yet (no ingest endpoints). Define the `trace_id TEXT NOT NULL UNIQUE` column in schema DDL, but actual UUID generation is used in Phase 2 ingest handlers. Include `google/uuid` in `go.mod` from Phase 1 so it's available.

### Pattern 9: Health Endpoint Response

**What:** The health handler assembles a JSON response from atomic counters (maintained by the ingest channel — to be added in Phase 2) and `os.Stat` calls for DB file sizes. In Phase 1, queue stats return zero values. Always return HTTP 200.

```go
// Source: CONTEXT.md health endpoint contract
package server

import (
    "encoding/json"
    "net/http"
    "os"
    "time"
)

type HealthResponse struct {
    // Core
    Status        string  `json:"status"`          // "ok" | "degraded" | "error"
    UptimeSeconds float64 `json:"uptime_seconds"`
    Version       string  `json:"version"`

    // DB
    DBStatus    string `json:"db_status"`    // "ok" | "error"
    DBSizeBytes int64  `json:"db_size_bytes"`
    WALSizeBytes int64 `json:"wal_size_bytes"`

    // Queue (Phase 2+ will populate these)
    QueueDepth     int   `json:"queue_depth"`
    EventsReceived int64 `json:"events_received"`
    EventsDropped  int64 `json:"events_dropped"`

    // Push (Phase 4+ will populate these)
    LastPushTime   *int64  `json:"last_push_time,omitempty"`  // Unix ms, null if never pushed
    LastPushStatus string  `json:"last_push_status,omitempty"`
    UnsyncedCount  int64   `json:"unsynced_count"`
}

type HealthHandler struct {
    dbPath    string
    startTime time.Time
    version   string
    // Phase 2+ will add: ingestChan chan Event, counters *atomic.Int64
}

func (h *HealthHandler) Handle(w http.ResponseWriter, r *http.Request) {
    resp := HealthResponse{
        Status:        "ok",
        UptimeSeconds: time.Since(h.startTime).Seconds(),
        Version:       h.version,
    }

    // Check DB file exists and get size
    if fi, err := os.Stat(h.dbPath); err != nil {
        resp.Status = "degraded"
        resp.DBStatus = "error"
    } else {
        resp.DBStatus = "ok"
        resp.DBSizeBytes = fi.Size()
    }

    // Check WAL file size
    if fi, err := os.Stat(h.dbPath + "-wal"); err == nil {
        resp.WALSizeBytes = fi.Size()
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK) // Always 200 — per CONTEXT.md contract
    json.NewEncoder(w).Encode(resp)
}
```

### Anti-Patterns to Avoid

- **`os.Exit()` in goroutines:** If config validation fails in a goroutine, return the error up to `main()`. Only `main()` should call `os.Exit`. `defer` cleanup is bypassed by `os.Exit`.
- **`http.ListenAndServe` without timeouts:** Always use `http.Server` struct with all four timeouts (ReadHeader, Read, Write, Idle).
- **Unbuffered signal channel:** Do not use `signal.Notify(ch, ...)` with `ch := make(chan os.Signal)`. Use `signal.NotifyContext` instead.
- **Applying pragmas only once on `db.Open`:** SQLite resets pragmas on new connections. Use `RegisterConnectionHook` so every connection in the pool gets them.
- **Single `*sql.DB` for both reads and writes:** Use two separate instances. Writer: `MaxOpenConns(1)`. Reader: `MaxOpenConns(4)`. WAL mode enables this safely.
- **`PRAGMA journal_mode=WAL` without `PRAGMA busy_timeout=10000`:** WAL alone does not prevent `SQLITE_BUSY`. Both must be set together.
- **Direct `log.Fatal` in startup:** Use `log.Fatal` only in `main()` during the startup phase (before goroutines are running). Once goroutines are started, propagate errors via channels/return values.
- **Conflating `unixepoch()` with Unix milliseconds:** SQLite's `unixepoch()` function returns Unix seconds. For Unix milliseconds, use `unixepoch() * 1000` or pass the value from Go: `time.Now().UnixMilli()`.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Env var parsing with struct defaults | Custom `os.Getenv` + strconv dance | sethvargo/go-envconfig | Handles types (Duration, int, bool), required vs optional, pointer semantics, nested structs |
| Signal handling with buffered channel | `make(chan os.Signal, 1); signal.Notify(...)` manual pattern | `signal.NotifyContext` | Handles buffering internally; integrates with context cancellation; properly resets handler |
| Log level parsing from string | `switch strings.ToLower(s)` | `slog.LevelVar.UnmarshalText` | Handles case insensitivity, returns proper slog.Level, handles "WARN" vs "warning" |
| UUID generation | `fmt.Sprintf("%x-%x-...", rand.Read(...))` | `github.com/google/uuid` | Correct v4 generation, proper entropy source, RFC 4122 compliant string format |
| SQLite pragma application per connection | `db.Exec("PRAGMA ...")` in each handler | `sqlite.RegisterConnectionHook` | Runs on every new connection automatically; cannot be forgotten; handles pool replenishment |
| HTTP server timeout configuration | Rolling custom timeout middleware | `http.Server{ReadTimeout: ...}` struct | Native, zero-cost, applies to all handlers including health |

**Key insight:** The "don't hand-roll" items in Phase 1 are all about correctness-by-construction. The Go stdlib and these minimal dependencies handle edge cases (signal buffering, connection pool pragma drift, UUID entropy) that are easy to get wrong the first time and hard to debug when they fail silently.

---

## Common Pitfalls

### Pitfall 1: SQLite Pragmas Not Applied on Every Connection

**What goes wrong:** `RegisterConnectionHook` is global. If you call `sql.Open` before the hook is registered, the first connection in the pool will not have WAL mode set. `PRAGMA journal_mode=WAL` on a new DB is safe, but if the DB was previously opened without WAL mode, the pragma must be applied on the first connection.

**Why it happens:** `RegisterConnectionHook` registers the hook for future connections. The hook function runs after the low-level connection is established. If the `init()` function runs after `sql.Open()`, you may miss the first connection.

**How to avoid:** Register the hook in an `init()` function in the `db` package — this guarantees it runs before any `sql.Open` call that imports the package. Verify WAL mode by querying `PRAGMA journal_mode` after open and logging the result.

**Warning signs:** `PRAGMA journal_mode` query returns `"delete"` instead of `"wal"`.

### Pitfall 2: `PRAGMA auto_vacuum` Must Be Set Before First Table Creation

**What goes wrong:** `PRAGMA auto_vacuum` can only be changed on a fresh (empty) database. Once any tables exist, the auto_vacuum setting is fixed and the pragma change is silently ignored. If you set `PRAGMA auto_vacuum = INCREMENTAL` after creating tables, it has no effect.

**Why it happens:** SQLite stores the auto_vacuum mode in the database header, which is written when the first page is allocated. The header cannot be changed without a VACUUM.

**How to avoid:** The `RegisterConnectionHook` approach applies pragmas including `auto_vacuum` before any DDL runs. But if the DB file already exists (second boot), `auto_vacuum` is already fixed from first boot. This is correct — just ensure it's set correctly on first boot. Verify with `PRAGMA auto_vacuum` after open.

**Warning signs:** `PRAGMA auto_vacuum` returns `0` (NONE) instead of `2` (INCREMENTAL) after setup.

### Pitfall 3: `db.Close()` Does Not Checkpoint WAL Automatically

**What goes wrong:** When `db.Close()` is called, modernc.org/sqlite does not automatically run a WAL checkpoint. The WAL file remains. On next boot, the WAL is applied during recovery, but the WAL file size at shutdown is not reclaimed.

**Why it happens:** WAL checkpoint behavior on close is driver-dependent. The Go `database/sql` `db.Close()` just closes connections; it does not issue any special SQLite close-time commands.

**How to avoid:** Before calling `db.Close()` in the shutdown sequence, explicitly run `PRAGMA wal_checkpoint(TRUNCATE)`. This blocks until all WAL pages are checkpointed and truncates the WAL file to zero.

**Code:**
```go
// In shutdown sequence, before db.Close()
if _, err := writerDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
    slog.Warn("wal checkpoint on shutdown failed", "error", err)
}
writerDB.Close()
readerDB.Close()
```

### Pitfall 4: go.mod Module Path Must Match Usage

**What goes wrong:** If the module path in `go.mod` is `module openclaw-trace` (without a domain), any import of internal packages as `openclaw-trace/internal/config` will work locally but fails if the module is ever published or imported externally. More practically: GoReleaser and Docker builds require the module path to match.

**How to avoid:** Use `module github.com/kon-rad/openclaw-trace` (or the appropriate organization path) in `go.mod`. This is the convention for Go modules and avoids path resolution issues.

### Pitfall 5: slog.SetDefault Does Not Affect Already-Created Loggers

**What goes wrong:** `slog.SetDefault(logger)` only affects calls to package-level functions like `slog.Info(...)`. Code that holds a reference to a previously created `*slog.Logger` is not affected. If multiple packages create their own loggers before `Setup()` runs, some log output will bypass the configured handler.

**How to avoid:** Call `slog.SetDefault` once at the very start of `main()`, before any other initialization that might log. Pass the logger explicitly to components that need it, rather than relying on the default. In Phase 1, there is very little logging before setup — this is easy to get right.

### Pitfall 6: Health Endpoint Must Handle Missing WAL File

**What goes wrong:** The WAL file (`trace.db-wal`) only exists after the first write transaction commits. On a freshly initialized database with no data yet, `os.Stat(dbPath + "-wal")` returns `os.ErrNotExist`. If the handler does not handle this, it will erroneously report WAL status as an error on a healthy fresh database.

**How to avoid:** Use `errors.Is(err, os.ErrNotExist)` to distinguish "file doesn't exist" (return 0 bytes, not an error) from "real stat error" (log a warning).

```go
if fi, err := os.Stat(h.dbPath + "-wal"); err == nil {
    resp.WALSizeBytes = fi.Size()
} else if !errors.Is(err, os.ErrNotExist) {
    slog.Warn("failed to stat WAL file", "error", err)
}
```

---

## Code Examples

Verified patterns from official sources and research:

### Complete Main Structure (Phase 1 skeleton)

```go
// Source: patterns from signal package + http.Server docs
package main

import (
    "context"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"

    "github.com/kon-rad/openclaw-trace/internal/config"
    "github.com/kon-rad/openclaw-trace/internal/db"
    "github.com/kon-rad/openclaw-trace/internal/logging"
    "github.com/kon-rad/openclaw-trace/internal/server"
)

var (
    version = "dev"
    commit  = "unknown"
)

func main() {
    // 1. Parse flags first (--help, --version exit before any init)
    setupFlags(version, commit)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    // 2. Load config
    cfg, err := config.Load(ctx)
    if err != nil {
        fmt.Fprintf(os.Stderr, "config error: %v\n", err)
        os.Exit(1)
    }

    // 3. Setup logging
    logger, err := logging.Setup(cfg.LogLevel)
    if err != nil {
        fmt.Fprintf(os.Stderr, "logging error: %v\n", err)
        os.Exit(1)
    }

    // 4. Startup banner
    slog.Info(fmt.Sprintf("openclaw-trace %s starting...", version))
    slog.Info("config",
        "port", cfg.Port,
        "db_path", cfg.DBPath,
        "log_level", cfg.LogLevel,
        "push_endpoint", maskSensitive(cfg.PushEndpoint),
        "push_interval", cfg.PushInterval,
        "retention_days", cfg.RetentionDays,
    )
    if cfg.PushEndpoint == "" {
        slog.Warn("OCT_PUSH_ENDPOINT is not set — push to remote is disabled")
    }

    // 5. Open SQLite
    startTime := time.Now()
    writerDB, readerDB, err := db.OpenDB(cfg.DBPath)
    if err != nil {
        slog.Error("failed to open database", "error", err)
        os.Exit(1)
    }
    defer func() {
        // WAL checkpoint before close (in shutdown sequence)
        if _, err := writerDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
            slog.Warn("wal checkpoint failed", "error", err)
        }
        writerDB.Close()
        readerDB.Close()
    }()

    // 6. Apply schema
    if err := db.ApplySchema(writerDB); err != nil {
        slog.Error("schema migration failed", "error", err)
        os.Exit(1)
    }
    slog.Info("SQLite opened", "path", cfg.DBPath, "mode", "WAL", "tables", 4)

    // 7. Build HTTP server
    healthHandler := server.NewHealthHandler(cfg.DBPath, startTime, version)
    srv := server.New(":"+cfg.Port, healthHandler)

    // 8. Start HTTP server in goroutine
    var wg sync.WaitGroup
    wg.Add(1)
    go func() {
        defer wg.Done()
        slog.Info("listening", "addr", ":"+cfg.Port)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            slog.Error("HTTP server error", "error", err)
        }
    }()

    // 9. Wait for shutdown signal
    <-ctx.Done()
    stop()
    slog.Info("SIGTERM received, shutting down...")

    // 10. Shutdown HTTP server
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if err := srv.Shutdown(shutdownCtx); err != nil {
        slog.Warn("HTTP shutdown error", "error", err)
    }

    // 11. Wait for all goroutines
    done := make(chan struct{})
    go func() { wg.Wait(); close(done) }()
    select {
    case <-done:
    case <-time.After(15 * time.Second):
        slog.Warn("goroutine drain timeout")
    }

    slog.Info("shutdown complete",
        "uptime", time.Since(startTime).String(),
    )
}
```

### Schema Application (idempotent)

```go
// Source: SQLite docs + CONTEXT.md schema decisions
package db

import "database/sql"

func ApplySchema(db *sql.DB) error {
    ddl := []string{
        createLLMTraces,
        createErrorEvents,
        createSystemMetrics,
        createPushLog,
        createIndexes,
    }
    for _, stmt := range ddl {
        if _, err := db.Exec(stmt); err != nil {
            return fmt.Errorf("schema DDL failed: %w\nSQL: %s", err, stmt)
        }
    }
    return nil
}
```

### Build Command

```bash
# Source: STACK.md research + Go docs
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build \
  -trimpath \
  -ldflags "-w -s -X main.version=v0.1.0 -X main.commit=$(git rev-parse --short HEAD)" \
  -o openclaw-trace \
  ./cmd/openclaw-trace/
```

### GOMEMLIMIT Configuration (Dockerfile or start.sh)

```bash
# Source: PITFALLS.md M-4 + weaviate.io/blog/gomemlimit
# Set to ~80% of container RAM. For 256MB container:
export GOMEMLIMIT=200MiB
# For 512MB container:
export GOMEMLIMIT=400MiB
# Or in Dockerfile:
ENV GOMEMLIMIT=200MiB
```

**Note (Go 1.26):** A proposal to auto-detect cgroup memory limits for GOMEMLIMIT is in progress (GitHub issue #75164) but was not in Go 1.26. Still set explicitly for now.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `mattn/go-sqlite3` (CGo required) | `modernc.org/sqlite` (pure Go) | Established ~2020, production-ready by 2023 | CGO_ENABLED=0 static builds work; cross-compilation works |
| `signal.Notify(make(chan os.Signal), ...)` | `signal.NotifyContext(ctx, ...)` | Go 1.16 (2021) | Integrates with context; internal buffering; cleaner API |
| Manual HTTP method checking | Go 1.22+ ServeMux method-prefixed patterns | Go 1.22 (2024) | `"GET /health"` native; 405 auto-response; no third-party router |
| `kelseyhightower/envconfig` | `sethvargo/go-envconfig` v1 | v1.0 released Dec 2023 | Context support, maintained, zero transitive deps |
| `logrus`, `zerolog` custom setup | `log/slog` (stdlib, Go 1.21+) | Go 1.21 (2023) | Zero-dep structured logging; JSON handler built-in; LevelVar |
| GOGC tuning only | `GOMEMLIMIT` (Go 1.19+) | Go 1.19 (2022) | Prevents OOM kills in containers; GC is cgroup-aware |
| `goreleaser` v1 | `goreleaser` v2 | GoReleaser v2 (2024) | Breaking config changes from v1; current docs use v2 syntax |

**Deprecated/outdated:**
- `logrus`: Unmaintained; replaced by `slog` for new Go projects
- `kelseyhightower/envconfig`: Not actively maintained; `sethvargo/go-envconfig` is the successor
- `signal.Notify` with unbuffered channel: Replaced by `signal.NotifyContext`
- `mattn/go-sqlite3` for static builds: `modernc.org/sqlite` is the correct choice

---

## Open Questions

1. **go.mod module path**
   - What we know: Convention is `github.com/{org}/{repo}`; the project is at `github.com/kon-rad/openclaw-trace` based on the hexly org structure
   - What's unclear: Whether this will be a public module or kept private; if private, a placeholder like `github.com/kon-rad/openclaw-trace` still works for local builds
   - Recommendation: Use `module github.com/kon-rad/openclaw-trace` and adjust if the actual repository path differs

2. **`OCT_PUSH_ENDPOINT` empty value behavior in health response**
   - What we know: CONTEXT.md says "warn if empty, don't refuse to start"; health endpoint returns `last_push_status` field
   - What's unclear: What value should `last_push_status` return when push is disabled from the start? `"disabled"` vs `""` vs omitting the field?
   - Recommendation: Return `"disabled"` as the `last_push_status` when `OCT_PUSH_ENDPOINT` is empty; this is more explicit than a null/empty string

3. **`push_log` table scope in Phase 1**
   - What we know: Phase 1 creates all 4 tables; `push_log` is not written to in Phase 1 (push is Phase 4)
   - What's unclear: Should the health endpoint's `unsynced_count` query the DB in Phase 1 or always return 0?
   - Recommendation: In Phase 1, return 0 for `unsynced_count` without querying DB. Add the real query in Phase 4 when push is implemented.

4. **`OCT_MAX_TEXT_BYTES` default value**
   - What we know: SUMMARY.md flags this as unresolved; CONTEXT.md adds it to config but doesn't specify default
   - What's unclear: The right default (4KB, 16KB, 32KB)
   - Recommendation: Default to `16384` (16KB) — enough for typical Claude claude-sonnet-4 responses while preventing extreme bloat from very long context windows

---

## Sources

### Primary (HIGH confidence)
- [pkg.go.dev/github.com/sethvargo/go-envconfig](https://pkg.go.dev/github.com/sethvargo/go-envconfig) — struct tag syntax, Process() API, required/default tags
- [pkg.go.dev/modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — driver name "sqlite", RegisterConnectionHook, DSN parameters
- [pkg.go.dev/log/slog](https://pkg.go.dev/log/slog) — JSONHandler, HandlerOptions, LevelVar, UnmarshalText
- [pkg.go.dev/flag](https://pkg.go.dev/flag) — Usage customization, flag.Parse, flag.Bool
- [pkg.go.dev/github.com/google/uuid](https://pkg.go.dev/github.com/google/uuid) — uuid.New(), uuid.NewRandom(), String() method
- [go.dev/blog/routing-enhancements](https://go.dev/blog/routing-enhancements) — Go 1.22+ ServeMux method-prefixed patterns
- [sqlite.org/wal.html](https://sqlite.org/wal.html) — WAL mode behavior, checkpoint types, WAL auto-checkpoint
- [sqlite.org/pragma.html](https://sqlite.org/pragma.html) — busy_timeout, auto_vacuum, synchronous, cache_size behavior

### Secondary (MEDIUM confidence)
- [theitsolutions.io/blog/modernc.org-sqlite-with-go](https://theitsolutions.io/blog/modernc.org-sqlite-with-go) — RegisterConnectionHook pattern with pragma block, writer/reader pool setup — verified against pkg.go.dev docs
- [victoriametrics.com/blog/go-graceful-shutdown/](https://victoriametrics.com/blog/go-graceful-shutdown/) — signal.NotifyContext + http.Server.Shutdown pattern — verified against Go stdlib docs
- [weaviate.io/blog/gomemlimit-a-game-changer](https://weaviate.io/blog/gomemlimit-a-game-changer-for-high-memory-applications) — GOMEMLIMIT behavior and 80% rule — verified against Go runtime docs
- [blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/](https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/) — four timeout fields — verified against net/http docs

### Tertiary (LOW confidence, flagged)
- Various blog posts on Go project layout — recommendations are largely consistent but are community convention, not official Go mandates

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries verified against pkg.go.dev with version numbers; Go 1.26 is current stable
- Architecture: HIGH — patterns derived from official Go docs and STACK.md/ARCHITECTURE.md research (already conducted)
- SQLite schema: HIGH — DDL derived from CONTEXT.md decisions + SQLite official docs on type affinity and pragma behavior
- Pitfalls: HIGH — drawn from PITFALLS.md (already researched) plus new Phase 1-specific additions verified against sources
- Code examples: MEDIUM — patterns verified against official sources but not compiled/tested; treat as design sketches

**Research date:** 2026-02-23
**Valid until:** 2026-05-23 (stable libraries; Go 1.26 released Feb 2026, next release ~Aug 2026)

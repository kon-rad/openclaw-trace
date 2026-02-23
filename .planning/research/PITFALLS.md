# Pitfalls Research: openclaw-trace

> Research date: 2026-02-23
> Project: Go sidecar binary providing full observability for OpenClaw AI agent instances.
> Target: <20MB RSS, <1% CPU, single static binary, Linux/amd64 on Fly.io.

---

## Critical Pitfalls (will break the project)

These are showstoppers. If not handled from Phase 1, they will produce data loss, OOM kills,
or silent failure.

---

### C-1: SQLite "database is locked" (SQLITE_BUSY) with default settings

**The trap**: The default SQLite busy timeout is 0ms. Any concurrent access from the HTTP
ingest goroutine and the periodic push goroutine hitting the same connection will immediately
return SQLITE_BUSY rather than waiting. With `database/sql` connection pooling and
multiple goroutines, this happens constantly.

**Warning signs**:
- Log lines with `database is locked` or `SQLITE_BUSY`
- Dropped trace events under any write load
- Inconsistent row counts between inserts and reads

**Prevention strategy**:
1. Enable WAL mode immediately on open: `PRAGMA journal_mode=WAL`
2. Set busy timeout to at least 10 seconds: `PRAGMA busy_timeout=10000`
3. Use `SetMaxOpenConns(1)` on the writer `*sql.DB` — SQLite is a single-writer DB;
   a pool of writers does not help and makes SQLITE_BUSY more likely
4. Use a separate read `*sql.DB` (with a higher `MaxOpenConns`) for queries during push
5. Prefer `BEGIN IMMEDIATE` for write transactions: avoids upgrade-deadlock where a read
   transaction tries to upgrade to a write lock and SQLITE_BUSY is returned even with a timeout

**Phase**: Address in Phase 1 (SQLite initialization code). Non-negotiable.

**References**:
- [SQLite concurrent writes and "database is locked"](https://tenthousandmeters.com/blog/sqlite-concurrent-writes-and-database-is-locked-errors/)
- [SQLITE_BUSY despite timeout — Bert Hubert](https://berthub.eu/articles/posts/a-brief-post-on-sqlite3-database-locked-despite-timeout/)
- [Go + SQLite best practices — Jake Gold](https://jacob.gold/posts/go-sqlite-best-practices/)

---

### C-2: WAL file grows without bound on long-running processes

**The trap**: WAL auto-checkpoint only runs when the same connection that committed a
transaction also happens to be the one to trigger the 1000-page threshold. With multiple
goroutines using separate connections, an active read transaction prevents WAL pages from
being checkpointed — the WAL grows indefinitely. On a 1GB Fly.io `/data` volume shared with
OpenClaw state, a runaway WAL will fill the disk and kill the entire agent.

**Warning signs**:
- `trace.db-wal` file growing beyond a few MB
- Disk usage climbing on the `/data` volume
- Eventual `database or disk is full` SQLite errors

**Prevention strategy**:
1. Run a background goroutine that issues `PRAGMA wal_checkpoint(PASSIVE)` every
   5–10 minutes — this is non-blocking and lets in-progress reads continue
2. Set `PRAGMA wal_autocheckpoint=400` (lower than the 1000-page default) to trigger
   more frequent checkpoints
3. Monitor WAL file size via `os.Stat("trace.db-wal")` and log warnings above a threshold
4. Implement a hard stop: if WAL > 50MB, issue `PRAGMA wal_checkpoint(RESTART)` and log
   an alert — RESTART is blocking but necessary to prevent disk exhaustion

**Phase**: Phase 1 (SQLite init). Add WAL monitoring in Phase 2.

**References**:
- [SQLite WAL documentation](https://sqlite.org/wal.html)
- [WAL File Grows Past Auto Checkpoint Limit — SQLite Forum](https://sqlite.org/forum/info/a188951b80292831794256a5c29f20f64f718d98ed0218bf44b51dd5907f1c39)
- [SQLite Vacuuming the WALs — The Unterminiated String](https://www.theunterminatedstring.com/sqlite-vacuuming/)

---

### C-3: VACUUM requires 2x database size in free disk space

**The trap**: `VACUUM` copies the entire database to rebuild it, requiring up to 2x the current
database size in free disk space. On a shared 1GB Fly.io volume already holding OpenClaw
state and the agent's LLM conversation history, a naive `VACUUM` can fail with
`database or disk is full` — and if the disk was already near full, this kills the machine.
By default, VACUUM also uses `/tmp`, which may be on a separate (smaller) tmpfs.

**Warning signs**:
- VACUUM calls failing with `disk is full`
- Database file growing to tens of MB over weeks of operation
- `/data` volume usage approaching capacity

**Prevention strategy**:
1. Do NOT run VACUUM on a schedule without checking available disk first
2. Check available space before any VACUUM: `os.Statfs("/data")`; skip if free space < 2x
   current DB size
3. Prefer `PRAGMA auto_vacuum=INCREMENTAL` set at database creation — this reclaims free
   pages incrementally on writes without requiring a full VACUUM
4. As a secondary mechanism, implement data retention: DELETE rows older than N days on a
   schedule, then run `PRAGMA incremental_vacuum(500)` in small batches
5. If VACUUM is required, set `SQLITE_TMPDIR` env var to `/data` so temp files land on the
   same volume

**Phase**: Phase 1 (schema design must include auto_vacuum). Phase 2 (retention jobs).

**References**:
- [SQLite VACUUM: database or disk is full — Simon Willison](https://til.simonwillison.net/sqlite/vacum-disk-full)
- [VACUUM requires 2x space — SQLite docs](https://sqlite.org/lang_vacuum.html)

---

### C-4: CGo cross-compilation breaks the single-binary promise

**The trap**: `github.com/mattn/go-sqlite3` uses CGo. This breaks `GOARCH=arm64
GOOS=linux` cross-compilation from macOS unless you have a full cross-compiler toolchain
installed. It also prevents `CGO_ENABLED=0` static builds. If a Docker build uses
`CGO_ENABLED=0` and the binary includes `go-sqlite3`, the binary silently falls back to a
stub that panics at runtime with `Binary was compiled with CGO_ENABLED=0`.

**Warning signs**:
- `cgo: C compiler "x86_64-linux-gnu-gcc" not found` during cross-compile
- `Binary was compiled with CGO_ENABLED=0, go-sqlite3 requires cgo` panic at startup
- Binary size suddenly much larger when CGo is enabled (pulls in libc)

**Prevention strategy**:
1. Use `modernc.org/sqlite` (pure Go SQLite transpilation) from Day 1 — it supports
   `CGO_ENABLED=0`, works in cross-compilation, and produces a fully static binary
2. The trade-off is ~2x slower writes; this is acceptable for a sidecar doing append-heavy
   workloads at low volume
3. Add a CI check: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...`
4. Use `go build -trimpath -ldflags="-s -w"` to strip debug info and reduce binary size

**Phase**: Phase 1 (dependency selection). This is a build-time decision that cannot be
easily reversed.

**References**:
- [GoLab 2024: To CGo or not — cross-compiling SQLite](https://golab.io/talks/to-cgo-or-not-cross-compiling-a-sqlite-driver)
- [You don't need CGO to use SQLite](https://hiandrewquinn.github.io/til-site/posts/you-don-t-need-cgo-to-use-sqlite-in-your-go-binary/)
- [Building static binaries with Go on Linux — Eli Bendersky](https://eli.thegreenplace.net/2024/building-static-binaries-with-go-on-linux/)

---

### C-5: Data loss on unclean shutdown with in-memory event buffer

**The trap**: The fire-and-forget design means the HTTP handler returns 200 immediately
and queues the event in a channel for async SQLite writes. If the process is killed before
the channel is drained — which happens during Fly.io machine stops, deploys, OOM kills —
all buffered events in the channel are silently lost. For an observability tool, losing
trace data is a fundamental failure.

**Warning signs**:
- Events accepted by HTTP endpoint but not in SQLite after restart
- Gap in trace data correlated with machine restarts
- Channel buffer consistently near capacity under normal load

**Prevention strategy**:
1. Register a `SIGTERM` handler and a `context.WithCancel` root context from `main()`;
   signal all goroutines to stop accepting new events
2. On shutdown signal, drain the in-memory channel to SQLite before exiting — give it a
   bounded timeout (e.g., 10 seconds)
3. Size the channel buffer conservatively (e.g., 1000 events); if the channel is full,
   log a drop warning and return HTTP 429 to the caller so the agent knows to back off
4. Call `db.Close()` after draining — this ensures SQLite flushes its page cache and
   performs a final WAL checkpoint
5. Use `sync.WaitGroup` to wait for all writer goroutines to finish before `os.Exit`

**Phase**: Phase 1 (core architecture). Signal handling must be wired from the first commit.

**References**:
- [Graceful Shutdown in Go: Practical Patterns — VictoriaMetrics](https://victoriametrics.com/blog/go-graceful-shutdown/)
- [Implementing Graceful Shutdown in Go — RudderStack](https://www.rudderstack.com/blog/implementing-graceful-shutdown-in-go/)

---

## Moderate Pitfalls (will cause pain)

These degrade quality, cause debugging headaches, or lead to incorrect data. They won't
crash the project immediately but will surface in production.

---

### M-1: Container CPU metrics report host CPU count, not cgroup limit

**The trap**: Reading `runtime.NumCPU()` or parsing `/proc/cpuinfo` inside a Fly.io
container returns the host machine's CPU count (often 8, 16, or more), not the number of
CPUs the container is allowed to use. Similarly, calculating CPU utilization by reading
`/proc/stat` gives host-wide stats unless the code is cgroup-aware. A "0.3% CPU"
reading from `/proc/stat` on a 16-core host could be 5% of the container's actual
1-core quota — deeply misleading.

**Warning signs**:
- CPU percentage always reported as a very small number (< 0.1%)
- `runtime.NumCPU()` returns a number larger than the Fly.io machine's CPU count
- GOMAXPROCS not matching container limits

**Prevention strategy**:
1. Use `github.com/prometheus/procfs` which provides cgroup-aware CPU parsing for both
   cgroup v1 and v2
2. For container CPU quota, read from `/sys/fs/cgroup/cpu/cpu.cfs_quota_us` and
   `cpu.cfs_period_us` (v1) or `/sys/fs/cgroup/cpu.max` (v2)
3. Calculate CPU utilization relative to the container quota, not the host total
4. Consider `github.com/uber-go/automaxprocs` or rely on Go 1.25+ which sets
   GOMAXPROCS automatically based on cgroup CPU limits
5. Document which metric is being reported: "host CPU %" vs "container CPU %" to avoid
   user confusion in dashboards

**Phase**: Phase 2 (system metrics collector). Must be addressed before any metrics are
considered accurate.

**References**:
- [Container CPU Requests & Limits Explained with GOMAXPROCS — VictoriaMetrics](https://victoriametrics.com/blog/kubernetes-cpu-go-gomaxprocs/)
- [Go Runtime's Container Awareness — Medium](https://medium.com/@barankibarr/go-runtimes-container-awareness-a-deep-dive-into-how-it-works-4e7bdfad2335)
- [CPU throttling for containerized Go apps — Kanishk Singh](https://kanishk.io/posts/cpu-throttling-in-containerized-go-apps/)

---

### M-2: Retry storms during remote push failures

**The trap**: When the remote Augmi API is temporarily down or rate-limiting, naive
retry logic retries all failed batches immediately and simultaneously. If five 5-minute
push cycles have queued up, they all retry at the same interval, creating a thundering
herd that hammers the API further. A fixed retry interval also means a self-inflicted
DDoS against the backend whenever there's an outage.

**Warning signs**:
- Log entries showing hundreds of retries within a short window
- Remote API returning 429 but retries continuing at the same rate
- Memory growing during outage as batches accumulate in memory

**Prevention strategy**:
1. Implement exponential backoff with jitter: `wait = baseDelay * 2^attempt + rand(0, baseDelay)`
2. Cap max retry attempts (e.g., 5 attempts) and max wait (e.g., 5 minutes) per batch
3. If push fails after all retries, leave the data in SQLite (it is already persisted);
   the next scheduled push cycle will pick it up — do NOT hold failed batches in memory
4. Track a "last successful push" timestamp in SQLite; use it to determine the correct
   lookback window on the next push, preventing duplicate sends
5. Respect `Retry-After` headers from the remote API
6. Use a circuit breaker pattern: after N consecutive failures, skip push attempts for
   a cooldown period rather than retrying indefinitely

**Phase**: Phase 3 (remote push). Must be designed before first push implementation.

**References**:
- [RetryGuard: Preventing Self-Inflicted Retry Storms](https://arxiv.org/html/2511.23278v1)
- [Exponential Backoff — HackerOne](https://www.hackerone.com/blog/retrying-and-exponential-backoff-smart-strategies-robust-software)

---

### M-3: HTTP server connection leaks from missing timeouts

**The trap**: Go's `net/http` `http.ListenAndServe` with no timeout configuration is
a well-known production anti-pattern. An agent sending a malformed or very large body
without a content-length header can hold a goroutine and file descriptor open
indefinitely. Under any load, this produces goroutine and fd leaks. Since the sidecar
runs alongside other processes on the same Fly.io machine, leaked goroutines and file
descriptors affect the entire machine.

**Warning signs**:
- Goroutine count climbing monotonically in `pprof`
- `too many open files` kernel errors
- Memory growing slowly over days

**Prevention strategy**:
1. Always configure timeouts on `http.Server`:
   ```go
   srv := &http.Server{
       ReadHeaderTimeout: 5 * time.Second,
       ReadTimeout:       10 * time.Second,
       WriteTimeout:      10 * time.Second,
       IdleTimeout:       60 * time.Second,
   }
   ```
2. Set a `MaxHeaderBytes` limit (default 1MB is fine)
3. For the event ingest endpoint, reject bodies larger than a defined max (e.g., 1MB)
   using `http.MaxBytesReader`
4. Expose `/debug/pprof` goroutine endpoint on a separate internal port for
   monitoring goroutine counts during development

**Phase**: Phase 1 (HTTP server setup). Never use bare `http.ListenAndServe`.

**References**:
- [The complete guide to Go net/http timeouts — Cloudflare](https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/)
- [Standard net/http config will break your production — Simon Frey](https://simon-frey.com/blog/go-as-in-golang-standard-net-http-config-will-break-your-production/)

---

### M-4: GOMEMLIMIT not set — GC runs too late, causes OOM kill

**The trap**: Without `GOMEMLIMIT`, the Go GC targets a heap size based on live objects
and the GOGC multiplier. In a constrained container (256MB or 512MB RAM), the GC can
allow the heap to grow well past the container's memory limit before triggering — at which
point the Linux OOM killer sends SIGKILL, with no chance for graceful shutdown, losing
all buffered data.

**Warning signs**:
- Process killed with `signal: killed` in logs (OOM kill signature on Linux)
- Memory growing during high-event-volume periods
- No "graceful shutdown" log lines before process exit

**Prevention strategy**:
1. Set `GOMEMLIMIT` to ~80% of the container's RAM allocation. For a Fly.io machine
   with 256MB: `GOMEMLIMIT=200MiB`
2. With GOMEMLIMIT set, the GC will run more aggressively as memory approaches the
   limit, trading CPU for memory headroom
3. Also tune `GOGC=50` for a sidecar — slightly more aggressive GC reduces peak heap
   at the cost of a small CPU increase, which is acceptable at <1% idle target
4. Monitor RSS with the `/metrics` health endpoint using `runtime.MemStats.Sys`

**Phase**: Phase 1 (Dockerfile / start.sh env vars). Set before first deployment.

**References**:
- [GOMEMLIMIT is a game changer — Weaviate](https://weaviate.io/blog/gomemlimit-a-game-changer-for-high-memory-applications)
- [Go GC Guide](https://go.dev/doc/gc-guide)

---

### M-5: Duplicate events pushed to remote API on retry

**The trap**: The periodic push reads a batch from SQLite and sends it to the remote API.
If the network times out after the API has processed the batch but before the HTTP response
is received, the push worker sees a network error and retries. The remote API receives the
same events a second time. Without deduplication, the Augmi dashboard shows doubled LLM
cost and token counts.

**Warning signs**:
- Duplicate trace IDs in the remote database
- Aggregate cost metrics inexplicably higher than actual usage
- Remote API returning 200 but push worker logging errors

**Prevention strategy**:
1. Assign a UUID `trace_id` to every event at ingest time; store it in SQLite
2. Include `trace_id` in every push payload — the remote API can use it for idempotency
3. Implement a `pushed_at` timestamp column in SQLite; only mark as pushed after a
   confirmed HTTP 200/201 response
4. Never delete or mark events pushed based on a send attempt — only on confirmed receipt
5. Design the remote API endpoint to be idempotent on `trace_id` (document this
   requirement explicitly for the Augmi backend team)

**Phase**: Phase 1 (schema design — add `trace_id` from the beginning). Phase 3 (push
implementation must honor it).

---

### M-6: procfs parsing returns stale or host-level data in containers

**The trap**: `/proc/stat` is sampled at a point in time. CPU% must be calculated as a
delta between two samples with a known time interval. A naive first-read implementation
calculates CPU% on the first collection tick against a zero baseline — producing a
meaningless 100% CPU spike in the first metric. Additionally, `/proc/meminfo` and
`/proc/stat` always report host-level data, not container-scoped data; for accurate
container memory, you must read from cgroup files.

**Warning signs**:
- First CPU percentage metric is always 100% or near-100%
- Memory reported is the full host RAM (e.g., 32GB) not the container limit (e.g., 512MB)
- Metrics look correct on dev machine but wrong in production containers

**Prevention strategy**:
1. Initialize the procfs CPU sampler on startup but discard the first reading — only
   emit metrics from the second reading onward (after a real delta exists)
2. For memory, read from `/sys/fs/cgroup/memory/memory.usage_in_bytes` (v1) or
   `/sys/fs/cgroup/memory.current` (v2) for the container's actual usage
3. For memory limit, read from `memory.limit_in_bytes` (v1) or `memory.max` (v2)
4. Use `github.com/prometheus/procfs` rather than manual `/proc` parsing — it handles
   cgroup v1/v2 detection and edge cases correctly
5. Test metrics collection in a Docker container during development, not just on
   the dev host machine

**Phase**: Phase 2 (metrics collector). Critical correctness issue.

**References**:
- [prometheus/procfs package](https://pkg.go.dev/github.com/prometheus/procfs)

---

## Minor Pitfalls (will annoy)

These cause friction during development or produce incorrect data in edge cases, but
are easy to fix when discovered.

---

### N-1: time.Ticker not stopped causes goroutine leaks (pre Go 1.23)

**The trap**: In Go < 1.23, `time.NewTicker` holds a reference in the runtime that
prevents garbage collection if `Stop()` is never called. Every metric collection loop
that creates a ticker without stopping it leaks a goroutine. In a sidecar with many
collectors, this compounds quickly.

**Warning signs**:
- Goroutine count increasing slowly but consistently in `pprof`
- Memory growing in proportion to uptime with no clear allocation source
- `go vet` or `goleak` reports leaked goroutines in tests

**Prevention strategy**:
1. Always pair `ticker := time.NewTicker(d)` with `defer ticker.Stop()` in the same
   goroutine
2. Use `for range ticker.C` pattern (Go 1.22+) which is cleaner and less error-prone
3. Add `goleak` to integration tests: `defer goleak.VerifyNone(t)` catches leaks in CI
4. Prefer `signal.NotifyContext` over manual signal channel wiring — it handles cleanup
5. Pin the Go runtime version to >= 1.23 to benefit from auto-GC of stopped tickers,
   but still call `Stop()` explicitly for clarity

**Phase**: Phase 1 onwards. Use `goleak` from the first test.

**References**:
- [Go issue: stop requiring Timer/Ticker.Stop for GC](https://github.com/golang/go/issues/61542)
- [50,000 Goroutine Leak case study](https://skoredin.pro/blog/golang/goroutine-leak-debugging)

---

### N-2: modernc.org/sqlite is ~2x slower on writes than mattn/go-sqlite3

**The trap**: The pure-Go SQLite transpilation is approximately 2x slower on INSERT
workloads compared to the CGo-based driver. Under burst ingest (an agent making many
rapid LLM calls), the SQLite write path can become a bottleneck, causing the channel
buffer to fill and events to be dropped.

**Warning signs**:
- Channel buffer consistently near capacity during LLM API bursts
- HTTP 429 responses returned to the agent during high-traffic periods
- SQLite write latency > 5ms per event on a fast disk

**Prevention strategy**:
1. Batch inserts: accumulate events in a small in-memory slice (e.g., 50 events or
   100ms, whichever comes first) and write them in a single transaction
2. Use `database/sql` with prepared statements for repeated inserts — avoids query
   planning overhead per row
3. Set `PRAGMA synchronous=NORMAL` (instead of the default FULL) — for a sidecar, a
   small risk of data loss on power failure is acceptable and provides a significant
   write speedup
4. Profile actual write throughput with `modernc.org/sqlite` early — it may be
   sufficient for the low-event-rate use case (< 100 LLM calls/minute)

**Phase**: Phase 1 (test early). Phase 2 (batching optimization if needed).

**References**:
- [SQLite in Go, with and without cgo — Multiprocess Labs](https://datastation.multiprocess.io/blog/2022-05-12-sqlite-in-go-with-and-without-cgo.html)
- [Benchmarking SQLite Performance in Go](https://www.golang.dk/articles/benchmarking-sqlite-performance-in-go)

---

### N-3: HTTP server signal handling: unbuffered signal channel drops signals

**The trap**: `signal.Notify(c, os.Interrupt, syscall.SIGTERM)` with an unbuffered
channel `c` will silently drop signals if the goroutine is not actively reading from
the channel at the exact moment the signal arrives. This can cause the process to
ignore SIGTERM and be force-killed after `kill_timeout` on Fly.io.

**Warning signs**:
- Process doesn't log "received SIGTERM, shutting down" during Fly.io machine stops
- Data loss correlated with machine restarts (suggests hard kill rather than graceful exit)
- Fly.io logs show machine killed after timeout rather than clean exit

**Prevention strategy**:
1. Always use a buffered channel: `c := make(chan os.Signal, 1)` — this ensures the
   signal is queued even if the goroutine is busy
2. Prefer `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` (Go 1.16+) which
   handles buffering internally
3. Set `kill_timeout = 30` in `fly.toml` to give the sidecar 30 seconds to flush
   SQLite and close connections (default is only 5 seconds)

**Phase**: Phase 1.

**References**:
- [Signal Handling and Graceful Shutdown in Go](https://riad.run/go-signals-and-graceful-shutdown)

---

### N-4: Binary size exceeds target if dependencies are not trimmed

**The trap**: Adding a single heavy indirect dependency (e.g., an observability SDK
that pulls in gRPC, protobuf, and OpenTelemetry) can push the binary from 8MB to
40MB+. The project targets <15MB. Go binaries include all transitive dependencies
at link time.

**Warning signs**:
- `go build` produces a binary > 15MB
- `go mod graph | wc -l` shows an unexpectedly large dependency tree
- Build time growing beyond 30 seconds

**Prevention strategy**:
1. Use `go build -trimpath -ldflags="-s -w"` — strips debug symbols and DWARF info,
   typically saves 30–40%
2. Run `govulncheck` and `go mod tidy` regularly to remove unused dependencies
3. Measure binary size in CI: `ls -lh` output as a build artifact comment
4. Stick to the constraint: stdlib + `modernc.org/sqlite` + `prometheus/procfs` +
   `hashicorp/go-retryablehttp`. No frameworks.
5. Use `go tool nm <binary> | sort -k2 -n | tail -50` to identify the largest symbols
   contributing to binary size

**Phase**: Phase 1 onwards. Check binary size on every PR.

---

### N-5: Log output from metric collector floods container logs

**The trap**: A metric collector that logs every collection cycle at INFO level
generates ~12 log lines per minute per collector (at 5-second intervals). These logs
are captured by Fly.io's log aggregation and can make it harder to find real errors in
the OpenClaw gateway logs. They also consume compute budget for log ingestion.

**Warning signs**:
- `flyctl logs` flooded with "collected CPU metrics" style entries
- Genuine errors buried under routine collector output
- Augmi log pipeline showing unexpectedly high log volume from the sidecar

**Prevention strategy**:
1. Default log level to WARN for production; only log errors and notable events
2. Use structured logging (`log/slog` from stdlib) — makes filtering easy downstream
3. Log collector runs at DEBUG level only; gate on an env var like
   `TRACE_LOG_LEVEL=debug`
4. Only log when a metric crosses a threshold (e.g., disk > 80%) or when an error occurs

**Phase**: Phase 1 (logging setup). Log level must be configurable via env var.

---

## Go-Specific Gotchas

---

### G-1: database/sql connection pool and SQLite don't mix well

`database/sql` assumes a connection pool with multiple concurrent connections for
parallelism. SQLite is single-writer. Setting `db.SetMaxOpenConns` higher than 1 for
the writer pool causes SQLITE_BUSY under concurrency. The correct pattern is:

```go
// Writer pool — single connection
writerDB, _ := sql.Open("sqlite", dsn)
writerDB.SetMaxOpenConns(1)
writerDB.SetConnMaxLifetime(0) // never close

// Reader pool — multiple connections OK in WAL mode
readerDB, _ := sql.Open("sqlite", dsn+"?mode=ro")
readerDB.SetMaxOpenConns(4)
```

### G-2: context.WithCancel leaks if cancel() is never called

Every `context.WithCancel` or `context.WithTimeout` must have a corresponding
`defer cancel()`. Go provides a static analysis check via `go vet`, but it's easy to
miss in complex goroutine trees. The lint rule `contextcheck` catches this in CI.

### G-3: http.DefaultClient has no timeout

Using `http.Get()` or `http.DefaultClient.Do()` for the remote push uses the global
HTTP client which has no timeout by default. A hanging remote API will block the push
goroutine indefinitely:

```go
// Wrong
resp, err := http.Get(endpoint)

// Right
client := &http.Client{Timeout: 30 * time.Second}
resp, err := client.Do(req)
```

### G-4: json.Marshal on large payloads allocates heavily

Encoding a batch of 500 trace events to JSON allocates a large intermediate buffer.
For a binary targeting <20MB RSS, large allocations cause GC pressure. Use
`json.NewEncoder(buf)` with a pre-allocated `bytes.Buffer` (or `sync.Pool`) to reduce
allocations in the hot push path.

### G-5: os.Exit() bypasses defer statements

If any code path calls `os.Exit()` directly (e.g., from a config validation failure),
all deferred cleanup — including `db.Close()`, WAL checkpoints, and in-flight writes —
is bypassed. Use `log.Fatal()` only from `main()` during startup, not from goroutines.
Goroutines should return errors up to `main()` which controls the exit path.

### G-6: sync.Map is not always the right tool

`sync.Map` is optimized for read-heavy, write-rarely workloads. For the event
deduplication cache or metric aggregation, a `sync.RWMutex` + `map` is usually faster
and more predictable for mixed read/write patterns.

---

## Container / Fly.io Gotchas

---

### F-1: kill_timeout defaults to 5 seconds — not enough for SQLite flush

Fly.io sends the kill signal and then waits `kill_timeout` seconds before sending
SIGKILL. The default is 5 seconds. A SQLite WAL checkpoint plus in-flight HTTP request
drain can take 5–15 seconds. If the process is hard-killed mid-checkpoint, the WAL
file may be left in an inconsistent state.

**Fix**: Set `kill_timeout = 30` in `fly.toml` for the sidecar process. Since the
sidecar is started from `start.sh` alongside the main OpenClaw gateway, this must be
coordinated so the sidecar gets sufficient time.

---

### F-2: The ephemeral filesystem is reset on every deploy

All files written outside of `/data` (the persistent Fly.io volume) are lost on
redeploy or machine restart. If the SQLite database is stored at any path other than
`/data/`, all trace history is wiped on every deploy. The default working directory
is ephemeral.

**Fix**: Default database path to `/data/openclaw-trace.db`. Make it configurable via
`TRACE_DB_PATH` env var. Document clearly that moving the DB off `/data` loses data.

---

### F-3: Port 9090 may already be in use on some Fly.io machine configurations

Port 9090 is the Prometheus default scrape port and may be claimed by other processes
or Fly.io infrastructure tooling on some machine types.

**Fix**: Make the port configurable via `TRACE_PORT` env var. Default to 9090 but
document the conflict risk. Add startup logging that shows which port is being used.

---

### F-4: Fly.io machine SIGTERM behavior differs from Kubernetes

On Fly.io, the kill signal goes to PID 1 (the process started by the Docker
`ENTRYPOINT`). If the sidecar is started as a background process in `start.sh`
(e.g., `./openclaw-trace &`), it will receive SIGTERM through process group propagation
only if `start.sh` propagates signals. Many shell scripts do not forward signals to
background children.

**Fix**: In `start.sh`, use `exec` for the main process and send explicit signals to
the sidecar PID, or use a process manager like `s6-overlay` that handles child signal
propagation correctly. Alternatively, design the sidecar to respond to both SIGTERM and
SIGINT and test signal propagation in CI.

---

### F-5: /data volume I/O bandwidth is limited (8MiB/s write)

Fly.io persistent volumes have a maximum write throughput of approximately 8MiB/s.
Under a burst of large LLM trace events (long input/output text), a naive synchronous
write-per-event pattern could approach this limit.

**Fix**: Batch writes (addressed in N-2). Store compressed payloads if event text is
large — consider storing only hash/length of input/output text for very large events,
with a configurable truncation limit (e.g., `TRACE_MAX_TEXT_BYTES=4096`).

---

## Prevention Checklist

### Phase 1 — Core Architecture

- [ ] **SQLite**: Use `modernc.org/sqlite` (no CGo); set WAL mode, busy_timeout=10000,
      synchronous=NORMAL, auto_vacuum=INCREMENTAL on first open
- [ ] **SQLite**: Writer db pool with `MaxOpenConns(1)`; reader db pool separate
- [ ] **SQLite**: Use `BEGIN IMMEDIATE` for all write transactions
- [ ] **Schema**: Add `trace_id UUID` (primary key) to all event tables from Day 1
- [ ] **Schema**: Add `pushed_at TIMESTAMP` column for push tracking
- [ ] **Schema**: Default db path to `/data/openclaw-trace.db` (configurable via env)
- [ ] **HTTP server**: Configure all four timeouts (ReadHeader, Read, Write, Idle)
- [ ] **HTTP server**: Use `http.MaxBytesReader` on ingest endpoint
- [ ] **Signal handling**: Use buffered channel or `signal.NotifyContext`; implement
      graceful drain with bounded timeout (10s) before `db.Close()`
- [ ] **GOMEMLIMIT**: Set in Dockerfile/start.sh to 80% of container RAM allocation
- [ ] **Logging**: Use `log/slog`; default to WARN; gate DEBUG on `TRACE_LOG_LEVEL`
- [ ] **Build**: Verify `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` passes
- [ ] **Build**: Check binary size post-build; target <15MB
- [ ] **Port**: Default 9090, configurable via `TRACE_PORT`; log on startup
- [ ] **Fly.io**: Set `kill_timeout = 30` for the machine

### Phase 2 — Metrics & Data Management

- [ ] **CPU metrics**: Read from cgroup files, not `/proc/stat` host totals
- [ ] **CPU metrics**: Discard first sample; only emit delta-based percentages
- [ ] **Memory metrics**: Read from `/sys/fs/cgroup/memory.current` (v2) or
      `memory.usage_in_bytes` (v1)
- [ ] **WAL checkpoint**: Background goroutine running `PRAGMA wal_checkpoint(PASSIVE)`
      every 10 minutes
- [ ] **WAL size monitor**: Log warning if WAL > 10MB; run RESTART checkpoint if > 50MB
- [ ] **Disk check**: Before any VACUUM, verify free space > 2x db size
- [ ] **Data retention**: Schedule DELETE of rows older than 30 days (configurable)
- [ ] **Text truncation**: Enforce `TRACE_MAX_TEXT_BYTES` on stored input/output text
- [ ] **Goroutine leaks**: Add `goleak` to all integration tests

### Phase 3 — Remote Push

- [ ] **Retry**: Implement exponential backoff with jitter; cap at 5 retries
- [ ] **Retry**: Respect `Retry-After` header from remote API
- [ ] **Idempotency**: Include `trace_id` in push payload; document API idempotency requirement
- [ ] **Push state**: Only mark `pushed_at` on confirmed HTTP 200/201; never on network error
- [ ] **Deduplication**: Use `last_push_cursor` (row ID or timestamp) to bound push lookback
- [ ] **HTTP client**: Use `&http.Client{Timeout: 30s}` for push, never `http.DefaultClient`
- [ ] **Buffer**: Return HTTP 429 to caller when ingest channel is full
- [ ] **Circuit breaker**: Skip push cycles after N consecutive failures; resume after cooldown

---

*Sources consulted:*
- [Go goroutine leaks — Leapcell](https://leapcell.io/blog/understanding-and-debugging-goroutine-leaks-in-go-web-servers)
- [Go graceful shutdown patterns — VictoriaMetrics](https://victoriametrics.com/blog/go-graceful-shutdown/)
- [SQLite WAL documentation](https://sqlite.org/wal.html)
- [SQLite concurrent writes — Ten Thousand Meters](https://tenthousandmeters.com/blog/sqlite-concurrent-writes-and-database-is-locked-errors/)
- [SQLITE_BUSY despite timeout — Bert Hubert](https://berthub.eu/articles/posts/a-brief-post-on-sqlite3-database-locked-despite-timeout/)
- [modernc.org/sqlite package](https://pkg.go.dev/modernc.org/sqlite)
- [SQLite in Go with and without cgo — Multiprocess Labs](https://datastation.multiprocess.io/blog/2022-05-12-sqlite-in-go-with-and-without-cgo.html)
- [GoLab 2024: Cross-compiling SQLite driver](https://golab.io/talks/to-cgo-or-not-cross-compiling-a-sqlite-driver)
- [Building static Go binaries — Eli Bendersky](https://eli.thegreenplace.net/2024/building-static-binaries-with-go-on-linux/)
- [Container CPU and GOMAXPROCS — VictoriaMetrics](https://victoriametrics.com/blog/kubernetes-cpu-go-gomaxprocs/)
- [prometheus/procfs package](https://pkg.go.dev/github.com/prometheus/procfs)
- [Go net/http timeouts — Cloudflare](https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/)
- [Standard net/http config issues — Simon Frey](https://simon-frey.com/blog/go-as-in-golang-standard-net-http-config-will-break-your-production/)
- [GOMEMLIMIT — Weaviate](https://weaviate.io/blog/gomemlimit-a-game-changer-for-high-memory-applications)
- [Go GC Guide — Go Team](https://go.dev/doc/gc-guide)
- [RetryGuard: preventing retry storms](https://arxiv.org/html/2511.23278v1)
- [SQLite VACUUM disk full — Simon Willison](https://til.simonwillison.net/sqlite/vacum-disk-full)
- [Go Goroutines 7 Critical Pitfalls — Medium](https://medium.com/@harshithgowdakt/go-goroutines-7-critical-pitfalls-every-developer-must-avoid-with-real-world-solutions-a436ac0fb4bb)
- [Durable Background Execution with Go and SQLite — Three Dots Labs](https://threedots.tech/post/sqlite-durable-execution/)
- [50,000 goroutine leak case study](https://skoredin.pro/blog/golang/goroutine-leak-debugging)
- [Go issue: time.Ticker GC](https://github.com/golang/go/issues/61542)
- [SQLite performance tuning — phiresky](https://phiresky.github.io/blog/2020/sqlite-performance-tuning/)
- [Fly.io graceful shutdown options](https://community.fly.io/t/new-feature-graceful-vm-shutdown-options/504)

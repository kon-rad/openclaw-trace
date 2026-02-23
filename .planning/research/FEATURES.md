# Features Research: openclaw-trace

## Competitive Landscape

The AI agent observability and LLM tracing space has two distinct camps that openclaw-trace draws from:

**LLM-native platforms** (cloud-hosted, SDK-based, full-stack):
- **LangSmith** — Deep LangChain integration, zero-config tracing for LangChain/LangGraph apps, prompt versioning, dataset evaluation. Cloud-only, $0.50–$5/1K traces.
- **Langfuse** — Open-source (MIT), self-hostable, strong prompt version control, LLM-as-a-judge evaluation, sessions for multi-turn conversations. Cloud or self-hosted.
- **Helicone** — Proxy-based (route requests through their URL), no SDK needed. Caching (20–30% cost reduction claimed), rate limiting, 100+ provider support. Fastest integration.
- **Portkey** — AI gateway with observability baked in. Focuses on routing, fallback, load balancing across 250+ models. Observability is secondary to gateway features.
- **Braintrust** — Evaluation-first platform. Real-time quality scoring, "LLM-as-judge" automation, dataset management, regression testing from production traces.
- **Arize Phoenix** — Open-source (MIT), OpenTelemetry-native, RAG-pipeline monitoring, ML drift detection. Positioned for data science teams.
- **AgentOps** — Python SDK, time-travel debugging, session replay. Agent-specific focus.

**Traditional APM and system observability (infrastructure-layer)**:
- **Prometheus + node_exporter** — ~1000+ system metrics (CPU, memory, disk, network, processes), pull-based scrape model, <30MB RAM, <1% CPU. Written in Go.
- **Datadog Agent** — Full-stack APM, distributed tracing, system metrics, logs. Enterprise pricing ($20K–$100K+/year). Head-based sampling, error sampling.
- **Grafana Agent** — Collector/forwarder for Prometheus, Loki, Tempo. Sidecar-friendly, lightweight, push and pull support.
- **OpenTelemetry Collector** — Standard collector sidecar. Receives spans via OTLP, transforms, exports to backends.

**Go-specific observability primitives**:
- **`net/http/pprof`** — Serves profiling data at `/debug/pprof/`: CPU profiles, heap profiles, goroutine stacks, block profiles. Zero dependencies, stdlib only.
- **`expvar`** — Exposes custom counters and gauges at `/debug/vars` as JSON. Standard interface for public variables in Go servers. Auto-exports GC stats, command-line args.
- **`runtime/metrics`** — Go 1.16+ stable metrics API: GC cycles, heap allocations, goroutine counts, scheduler latency, memory by category. More precise than expvar.

**Emerging standard**: OpenTelemetry GenAI Semantic Conventions (in development as of 2026) define standard attribute names for LLM spans: `gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.response.finish_reasons`, etc. Adoption is growing but not yet table stakes.

---

## Table Stakes

Features users expect from any tracing/observability tool. Absence causes users to choose a competitor or build their own.

### LLM Call Logging

**What it is**: Capture every LLM API request with structured fields.

**Minimum viable fields** (per OpenTelemetry GenAI conventions):
- `provider` — anthropic, openai, etc.
- `model` — claude-3-5-sonnet-20241022, gpt-4o, etc.
- `prompt_tokens` / `completion_tokens` / `total_tokens`
- `latency_ms` — time from request to first byte and to completion
- `status` — success, error, timeout, rate_limited
- `error_type` — on failure (rate_limit, timeout, context_length, etc.)
- `cost_usd` — calculated from token counts and model pricing table
- `timestamp` — ISO 8601 UTC

**Complexity**: Medium. Requires a maintained cost-per-token lookup table per model/provider. Token counts come from the agent; no parsing needed if agent sends them.

**Dependencies**: HTTP endpoint to receive events. SQLite schema. Cost lookup table.

---

### Cost Tracking

**What it is**: Aggregate USD cost per call, per session, per day, per model. Every competing tool has this.

**Minimum**: Running total and per-call cost. Per-model breakdown.

**Nice to have**: Cost per user session, cost rate alerts, projected monthly spend.

**Complexity**: Low. Cost = (prompt_tokens * input_price + completion_tokens * output_price) per model. Requires a maintained pricing table (providers change prices; this is the maintenance burden).

**Dependencies**: LLM Call Logging. Model pricing lookup table (embedded JSON, refreshed periodically).

---

### Latency Tracking

**What it is**: How long each LLM call takes. Broken down by provider and model. Percentiles (p50, p95, p99) over time windows.

**Complexity**: Low. Timestamps on ingestion, simple math. Percentile queries on SQLite are possible with ORDER BY + LIMIT.

**Dependencies**: LLM Call Logging.

---

### Error Capture and Categorization

**What it is**: Catch and classify failures. Users need to know: what broke, when, how often, what kind.

**Minimum categories**:
- LLM API errors (rate limits, context length exceeded, auth failures, provider downtime)
- Timeout errors
- Agent process crashes / OOM kills
- System errors (disk full, network unreachable, permission denied)

**Complexity**: Medium. Requires the agent to send error events (simple HTTP POST) and the sidecar to detect system-level errors independently (process monitoring, disk space checks).

**Dependencies**: HTTP event endpoint. System metrics collection. Process monitoring.

---

### System Metrics: CPU and Memory

**What it is**: Host resource utilization. Every server monitoring tool captures this. Users need it to understand if their agent is resource-starved.

**Minimum**:
- CPU: utilization % (user, system, idle), load averages (1m, 5m, 15m)
- Memory: total, used, available, swap used
- Go runtime: heap size, GC count, goroutine count (for the tracer itself)

**Source**: `/proc/stat`, `/proc/meminfo` on Linux. Go `runtime/metrics` package.

**Complexity**: Low. Go standard library and `gopsutil` or direct `/proc` parsing. node_exporter pattern is well-established.

**Dependencies**: Background collection goroutine. SQLite storage.

---

### System Metrics: Disk

**What it is**: Disk space on the `/data` persistent volume — critical for Fly.io agents with 1GB volumes.

**Minimum**:
- Total, used, available bytes on `/data`
- Usage percentage (alert threshold)
- I/O rates (read/write bytes per second)

**Complexity**: Low. `syscall.Statfs` for space. `/proc/diskstats` for I/O rates.

**Dependencies**: Background collection goroutine. SQLite storage.

---

### HTTP Receive Endpoint

**What it is**: The primary ingestion interface. Agents POST trace events to `http://localhost:9090/v1/traces`. Fire-and-forget — agent does not wait for response.

**Minimum**: Accept JSON POST, validate schema loosely, write to SQLite, return 202 Accepted immediately. Non-blocking.

**Complexity**: Low. Standard Go `net/http` handler. Channel-based async write to SQLite.

**Dependencies**: SQLite writer. Schema definition.

---

### Local SQLite Storage

**What it is**: Durable, queryable local storage for all trace data before push. Survives tracer restarts. Enables historical queries.

**Minimum**: Tables for traces, system_metrics, errors, custom_events. Automatic cleanup (rolling retention, e.g., keep 7 days).

**Complexity**: Medium. Schema design, index selection, cleanup job. `modernc.org/sqlite` for pure-Go (no CGo) embedded SQLite.

**Dependencies**: None. Foundation for everything else.

---

### Periodic Remote Push

**What it is**: Batch-send accumulated data to a configurable remote API. Every observability agent does this.

**Minimum**: Configurable interval (default 5 min). JSON batch payload. Retry with exponential backoff. Mark records as pushed (or delete after push). Configurable endpoint URL.

**Complexity**: Medium. HTTP client with retry, backoff, timeout. Batch size limits. Handling partial failures. Idempotency key recommended.

**Dependencies**: SQLite storage. HTTP client.

---

### Health Check Endpoint

**What it is**: Lets external systems verify the tracer is alive. Required for any production sidecar. Fly.io health checks, dashboards, and monitoring depend on this.

**Minimum**: `GET /health` returns `{"status":"ok","uptime_seconds":N,"sqlite_ok":true}`. Returns 200 if healthy, 503 if degraded.

**Complexity**: Very low.

**Dependencies**: None beyond the HTTP server.

---

### Graceful Shutdown

**What it is**: On SIGTERM, flush pending data to SQLite and attempt one final push before exit. Prevents data loss during restarts/deploys.

**Complexity**: Low. Go `signal.NotifyContext` + drain channel pattern.

**Dependencies**: SQLite writer. Push client.

---

### Configuration via Environment Variables

**What it is**: All settings configurable via env vars. Required for containerized/sidecar deployment. Fly.io machines configure processes entirely via env.

**Minimum env vars**:
- `TRACER_PORT` (default: 9090)
- `TRACER_PUSH_INTERVAL` (default: 5m)
- `TRACER_PUSH_URL` (required)
- `TRACER_PUSH_API_KEY` (required)
- `TRACER_DB_PATH` (default: /data/tracer.db)
- `TRACER_RETENTION_DAYS` (default: 7)
- `TRACER_LOG_LEVEL` (default: info)

**Complexity**: Very low. `os.Getenv` with defaults.

**Dependencies**: None.

---

## Differentiators

Features that would set openclaw-trace apart from generic APM tools and LLM-native platforms.

### Custom Event API

**What it is**: Agents can POST arbitrary named events with structured metadata. "Agent started new task", "user approved action", "tool call executed", "memory saved". Not just LLM calls — the full agent lifecycle.

**Why differentiating**: Most LLM tools only track LLM API calls. AgentOps is the closest competitor here, but it's Python-only. openclaw-trace being language-agnostic (HTTP-based) and framework-agnostic is a moat.

**Design**:
```json
POST /v1/events
{
  "name": "tool_call_executed",
  "timestamp": "2026-02-23T10:00:00Z",
  "metadata": {"tool": "read_file", "path": "/data/config.json", "duration_ms": 12}
}
```

**Complexity**: Low. The hard work is in the SQLite schema and the remote push aggregation.

**Dependencies**: HTTP endpoint. SQLite schema with flexible JSONB-like metadata column (SQLite JSON1 extension).

---

### OpenClaw Gateway Log Parsing (Optional Enrichment Mode)

**What it is**: Read OpenClaw's gateway log files from the filesystem, parse structured log lines, and enrich trace data with gateway-level context (HTTP requests, session IDs, channel events).

**Why differentiating**: Zero code change required in OpenClaw itself. Works purely by reading log files the gateway already writes. Provides deeper context than just LLM API call data.

**Complexity**: Medium. Log tail goroutine + regex/JSON parsing + correlation with trace events by timestamp/session ID. Fragile against log format changes.

**Dependencies**: Filesystem access. Log rotation awareness (handle file truncation/rotation).

**Risk**: OpenClaw log format can change without warning. Mark as optional/experimental.

---

### Process Monitoring and Zombie Detection

**What it is**: Watch the running processes on the machine. Detect if the OpenClaw gateway process has died, is consuming excessive CPU/memory, or has become a zombie. Alert via error events.

**Why differentiating**: Traditional APM monitors services from outside. This sidecar monitors the sibling process directly, enabling "the agent is stuck" detection that Langfuse/Helicone cannot provide.

**Design**: Periodic scan of `/proc/[pid]/stat`, `/proc/[pid]/status`, `/proc/[pid]/io`. Detect zombie state (`Z` in stat). Track CPU/memory per process.

**Complexity**: Medium. Process enumeration on Linux via `/proc`. Finding the OpenClaw process by name/port.

**Dependencies**: System metrics collection loop. Error event pipeline.

---

### Network Metrics (Open Connections, Bandwidth)

**What it is**: Track outbound connections (to Anthropic, OpenAI, etc.), bandwidth consumed, DNS resolution times.

**Why differentiating**: Helps diagnose "why is the agent slow?" — is it the model latency or network? Are there unusual outbound connections? Not available in any LLM-native tool.

**Design**: Parse `/proc/net/tcp`, `/proc/net/tcp6` for connection states. Use `/proc/net/dev` for bandwidth. Periodic DNS probe to known endpoints.

**Complexity**: Medium-high. Parsing `/proc/net/*` is doable but requires care. DNS probing adds complexity.

**Dependencies**: Background metrics collector.

---

### Agent-Agnostic HTTP Interface

**What it is**: The sidecar speaks plain JSON over HTTP. Any agent written in any language (Node.js OpenClaw, Python, Ruby, whatever) can send events with a one-liner HTTP POST. No SDK required.

**Why differentiating**: Helicone requires URL routing. LangSmith/Langfuse require SDK integration. openclaw-trace requires nothing — just an HTTP call. This is the lowest possible integration friction.

**Complexity**: Low (it's the design, not extra code). The value is in NOT adding complexity.

**Dependencies**: HTTP server (already required).

---

### SQLite Retention with Volume-Aware Cleanup

**What it is**: The database knows it's living on a 1GB Fly.io volume. It automatically rotates old records when volume usage exceeds a threshold (e.g., 80% full), not just based on age.

**Why differentiating**: Generic observability agents don't know about constrained-volume deployment. This prevents the agent from running out of disk space due to trace accumulation.

**Design**: Background goroutine checks disk usage every N minutes. If usage > threshold, delete oldest records beyond min retention. Configurable thresholds.

**Complexity**: Low-medium. Add disk usage check to cleanup logic.

**Dependencies**: Disk metrics. SQLite cleanup job.

---

### Lightweight Go Runtime Self-Observability

**What it is**: The tracer exposes its own health metrics at `/debug/vars` (expvar) and `/debug/pprof` (pprof). Users can profile the tracer itself if it's causing resource issues.

**Why differentiating**: Most sidecar tools are black boxes. Exposing pprof endpoints lets advanced users diagnose the tracer itself — consistent with Go ecosystem norms. Builds trust in the <20MB RAM / <1% CPU claims.

**Complexity**: Very low. Import `_ "net/http/pprof"` and `_ "expvar"`. Register custom counters for traces_received, pushes_sent, errors_captured.

**Dependencies**: HTTP server (already required).

---

### Offline Operation and Push Resilience

**What it is**: The sidecar operates fully independently of remote connectivity. It stores data locally indefinitely (within retention limits) and retries pushes with exponential backoff. If the remote API is down for hours, no data is lost.

**Why differentiating**: Cloud-hosted tools like Helicone lose data if the proxy is unreachable. This sidecar is local-first — it's the single source of truth until data is confirmed pushed.

**Complexity**: Medium. Track push status per batch. Retry queue. Idempotency tokens in push payloads.

**Dependencies**: SQLite (push_status column). Retry client with backoff.

---

## Anti-Features

Things to deliberately NOT build in v1 — and why.

### Web Dashboard UI

**Why not**: The PROJECT.md explicitly excludes this. The Augmi platform dashboard at augmi.world handles visualization. Building a UI in the sidecar binary doubles the scope (serve HTML, JS, CSS), bloats the binary, and competes with the platform.

**What to do instead**: Expose clean JSON APIs. Let the dashboard consume them.

---

### Real-Time WebSocket / SSE Push

**Why not**: PROJECT.md marks this out of scope. Adds significant complexity (connection management, backpressure, reconnection handling) for marginal benefit. Periodic 5-minute push is sufficient for operational observability. Real-time monitoring can be approximated by polling the health endpoint.

**What to do instead**: Short configurable push interval (down to 30s) for users who need near-real-time.

---

### Distributed Tracing (Spans Across Services)

**Why not**: openclaw-trace tracks a single agent on a single machine. Distributed tracing (OpenTelemetry spans with parent/child relationships across services) is a different problem domain. It requires trace context propagation, a backend like Jaeger/Zipkin/Tempo, and SDK integration in every service. This is not a sidecar problem.

**What to do instead**: Use standard field names (`trace_id`, `span_id`) so data can be correlated later by a downstream system if needed. Don't implement the propagation protocol.

---

### Log Aggregation and Full-Text Search

**Why not**: This is a different product (Loki, Elasticsearch, Papertrail). openclaw-trace is a tracing and metrics tool. Adding log ingestion, indexing, and search turns it into a log management system — a much harder problem with much higher storage requirements.

**What to do instead**: Parse logs opportunistically for enrichment (the optional log parsing mode). Don't build log storage or search.

---

### LLM Evaluation / Quality Scoring

**Why not**: LangSmith, Langfuse, and Braintrust all do this. It requires LLM-as-judge calls (costs money), dataset management, and prompt versioning — none of which belong in a lightweight sidecar binary.

**What to do instead**: Capture `input_text` and `output_text` in trace events. Let the Augmi platform or the user run evaluations against the raw data.

---

### Prompt Management and Versioning

**Why not**: Platform-level feature. Requires a UI, version history, A/B testing workflow. Out of scope for a sidecar collector.

**What to do instead**: Include `prompt_version` as an optional field in the trace event schema. Store it. Let the platform build the versioning UI on top.

---

### Windows Support

**Why not**: Fly.io containers run Linux. openclaw-trace's entire value is on Linux (Fly.io machines). Windows support requires different `/proc` alternatives, different binary build pipeline, different install story. Cost-benefit is deeply negative.

**What to do instead**: Target Linux amd64/arm64 only. macOS support for local dev is acceptable (most `/proc` alternatives exist via `gopsutil`).

---

### Multi-Tenant / Multi-Agent Aggregation

**Why not**: Each sidecar runs alongside exactly one agent on one machine. Aggregation across agents is a platform problem (the Augmi dashboard). Building multi-agent awareness into the sidecar adds complexity with no benefit to the sidecar's core job.

**What to do instead**: Include `agent_id` in all trace events as a field. The remote API aggregates across agents.

---

### SDK / Library Integration (Go/Python/Node packages)

**Why not**: The fire-and-forget HTTP endpoint is the integration point. Publishing language-specific SDKs is a significant ongoing maintenance burden (versioning, breaking changes, docs, community support). The HTTP interface is already language-agnostic.

**What to do instead**: Publish excellent API documentation and a curl example. Let the community build client libraries if demand exists.

---

### Alerting / PagerDuty / Webhook Notifications

**Why not**: The Augmi platform should own alerting policy (thresholds, routing, escalation). The sidecar captures data; the platform decides what's actionable. Building alerting in the sidecar duplicates effort and requires configuration complexity (webhook URLs, retry, dedup).

**What to do instead**: Include `severity` and `alert_level` fields in error events. The remote API/platform handles notification.

---

### OpenTelemetry Collector Protocol (OTLP) Compatibility

**Why not** (for v1): Implementing the full OTLP gRPC/HTTP protocol is non-trivial. It adds proto dependencies, a more complex schema, and a steeper learning curve. The OpenClaw agent is the only current producer — a simpler JSON HTTP API is faster to ship and easier to understand.

**Revisit when**: Multiple heterogeneous producers need to send traces. Then OTLP compatibility becomes a significant interoperability win.

**What to do instead**: Use field names aligned with OpenTelemetry GenAI semantic conventions (e.g., `gen_ai.provider.name`, `gen_ai.usage.input_tokens`) so data can be mapped to OTEL format later without schema changes.

---

## Feature Dependencies

```
SQLite Local Storage
  └── LLM Call Logging
        ├── Cost Tracking
        ├── Latency Tracking
        └── Error Capture (partial)
  └── System Metrics: CPU/Memory
  └── System Metrics: Disk
  └── Custom Event API
  └── Process Monitoring

HTTP Receive Endpoint
  ├── LLM Call Logging (consumes events)
  ├── Custom Event API (consumes events)
  └── Health Check Endpoint (same server)

SQLite Local Storage + HTTP Receive Endpoint
  └── Periodic Remote Push
        └── Offline Operation / Push Resilience

System Metrics: CPU/Memory + System Metrics: Disk
  └── Volume-Aware SQLite Cleanup (needs disk usage)
  └── Process Monitoring (needs process list)
  └── Network Metrics (additive, same collection loop)

Environment Variable Configuration
  └── Everything (configuration is cross-cutting)

Graceful Shutdown
  └── SQLite Writer (must flush)
  └── Periodic Remote Push (must attempt final push)

OpenClaw Log Parsing (optional)
  └── LLM Call Logging (enriches existing trace records)
  └── Custom Event API (can emit parsed log events)
```

---

## Complexity Estimates

Relative scale: **XS** (hours) / **S** (1 day) / **M** (2–3 days) / **L** (1 week) / **XL** (2+ weeks)

| Feature | Complexity | Notes |
|---|---|---|
| SQLite local storage (schema + basic ops) | M | Schema design is the hard part. modernc.org/sqlite avoids CGo. |
| HTTP receive endpoint (LLM traces) | S | Standard Go net/http. Non-blocking channel write. |
| LLM call logging (fields + storage) | S | Depends on schema. Cost table lookup is the tricky part. |
| Cost tracking (per-call USD) | S | Needs maintained pricing table. Prices change; plan for refresh. |
| Latency tracking | XS | Timestamps + math. SQLite percentile queries via OFFSET/LIMIT. |
| Error capture (LLM API errors) | S | Agent sends error events; sidecar classifies by error_type field. |
| System metrics: CPU/memory | S | gopsutil or direct /proc parsing. Collection loop + SQLite writes. |
| System metrics: disk | XS | syscall.Statfs. One function call. |
| Periodic remote push with retry | M | Batch logic, retry/backoff, push status tracking, idempotency. |
| Health check endpoint | XS | One HTTP handler, three status checks. |
| Graceful shutdown | XS | signal.NotifyContext + channel drain. |
| Environment variable configuration | XS | os.Getenv with defaults. Struct population. |
| Custom event API | S | Same as LLM endpoint but flexible schema (JSON metadata column). |
| Volume-aware SQLite cleanup | S | Disk check + DELETE oldest. Configurable thresholds. |
| Go runtime self-observability (pprof/expvar) | XS | Two blank imports + custom counters. |
| Offline operation / push resilience | M | Push status tracking in SQLite. Retry queue. Idempotency tokens. |
| Process monitoring + zombie detection | M | /proc enumeration, PID tracking, state parsing. |
| Network metrics | M | /proc/net parsing. DNS probe goroutine. |
| OpenClaw log parsing (enrichment) | L | Tail goroutine + parser + correlation logic. Fragile. |
| OTLP protocol compatibility | XL | Proto deps, schema mapping, gRPC server. Defer to v2. |
| Prompt versioning / evaluation | XL | Platform feature. Never. |
| Web dashboard UI | XL | Out of scope forever. |

### Build Order Recommendation

**Phase 1 (v0.1 — get data flowing)**:
1. SQLite schema + writer
2. HTTP server (health + trace endpoint)
3. LLM call logging with cost tracking
4. Basic CPU/memory/disk metrics collection
5. Periodic remote push
6. Graceful shutdown + env config

**Phase 2 (v0.2 — completeness)**:
7. Custom event API
8. Error capture (LLM + system)
9. Volume-aware cleanup
10. Process monitoring
11. pprof/expvar self-observability

**Phase 3 (v0.3 — enrichment)**:
12. Network metrics
13. Push resilience (idempotency, retry queue)
14. OpenClaw log parsing (optional mode)

---

*Research completed: 2026-02-23. Sources: LangSmith, Langfuse, Helicone, Portkey, Braintrust, Arize Phoenix, AgentOps, Datadog APM, Prometheus node_exporter, OpenTelemetry GenAI semantic conventions, Go stdlib (pprof, expvar, runtime/metrics).*

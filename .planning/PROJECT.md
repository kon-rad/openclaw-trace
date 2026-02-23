# openclaw-trace

## What This Is

A lightweight, performant Go sidecar binary that provides full observability for OpenClaw AI agent instances. It tracks LLM API calls (provider, model, input/output text, token counts, cost), system metrics (CPU, RAM, disk, network, processes), and error logging — storing everything locally in SQLite and periodically pushing to a configurable remote API. Designed as an open-source package that anyone running OpenClaw (or similar AI agents) can drop in for instant traceability.

## Core Value

**Complete visibility into what an AI agent is doing, what it costs, and how healthy its host is — with zero impact on the agent's performance.**

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Sidecar binary runs alongside OpenClaw agent on Fly.io machines
- [ ] HTTP endpoint receives structured LLM call trace events (fire-and-forget from agent)
- [ ] Captures: provider, model, input text, output text, token count (prompt + completion), cost, latency, status
- [ ] System metrics collection: CPU load, RAM usage, Go runtime stats
- [ ] System metrics collection: disk usage, /data volume space, I/O rates
- [ ] System metrics collection: running processes, resource per process, zombie detection
- [ ] System metrics collection: open connections, bandwidth, DNS resolution times
- [ ] Error capture: LLM API failures, rate limits, timeouts, malformed responses
- [ ] Error capture: agent process crashes, OOM kills, unhandled exceptions
- [ ] Error capture: system errors (disk full, network failures, permission denied)
- [ ] Custom event API: agents can report arbitrary named events with metadata
- [ ] Local SQLite storage for all trace data
- [ ] Periodic push to configurable remote API (default: every 5 minutes, configurable via env var)
- [ ] Optional log parsing mode for enrichment (read OpenClaw gateway logs for additional context)
- [ ] Minimal resource footprint: target <20MB RAM, <1% CPU at idle
- [ ] Single static binary with zero external dependencies
- [ ] Configuration via environment variables and/or config file
- [ ] Health check endpoint for monitoring the tracer itself
- [ ] Graceful shutdown with flush of pending data
- [ ] MIT licensed, open source, designed for community adoption

### Out of Scope

- Built-in web dashboard UI — the Augmi platform dashboard handles visualization
- Real-time streaming/WebSocket push — periodic batch push is sufficient
- Distributed tracing (spans/traces across multiple services) — this tracks a single agent
- Log aggregation/search — this is tracing, not a log management system
- Windows support — targets Linux (Fly.io containers) primarily
- Agent code modifications — the tracer is a separate binary, not a library

## Context

- OpenClaw agents run as Node.js processes on Fly.io machines with 1GB persistent volumes at `/data`
- Each machine runs: OpenClaw Gateway (port 3000), Health Server (port 8080)
- The tracer sidecar would claim port 9090 (configurable)
- Agents can POST trace events to `http://localhost:9090/v1/traces` with fire-and-forget
- The Augmi platform at augmi.world has an API that can receive pushed trace data
- The remote push API endpoint is configurable so other users can point to their own backends
- OpenClaw gateway logs are available on the local filesystem for optional log parsing
- Fly.io machines have limited resources — the tracer MUST be lightweight
- The tracer binary will be added to the OpenClaw Docker image and started via `start.sh`

## Constraints

- **Language**: Go — for single-binary deployment, low resource usage, excellent concurrency
- **Binary size**: Target <15MB static binary (no CGo if possible, use modernc.org/sqlite)
- **Memory**: Target <20MB RSS under normal operation
- **CPU**: <1% at idle, brief spikes during push are acceptable
- **Storage**: SQLite database with automatic rotation/cleanup to respect volume limits
- **Dependencies**: Minimal — stdlib + SQLite driver. No heavy frameworks.
- **Compatibility**: Linux amd64 (Fly.io primary), arm64 secondary. macOS for dev.
- **API**: RESTful JSON API for receiving events and exposing metrics
- **Push format**: JSON batch payloads to remote API with retry and backoff

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Go language | Single binary, low footprint, great stdlib for HTTP/concurrency | — Pending |
| Sidecar architecture | Independent lifecycle, zero agent coupling, crash isolation | — Pending |
| SQLite local storage | Zero deps, reliable, good for time-series-like data with cleanup | — Pending |
| HTTP endpoint for LLM tracking | Standard pattern (OpenTelemetry-like), structured data, portable | — Pending |
| Fire-and-forget from agent | Agent never blocks waiting for tracer — zero latency impact | — Pending |
| Periodic batch push (5min default) | Balances freshness vs API cost. Configurable for different needs | — Pending |
| MIT license | Maximum adoption, no restrictions, matches Go ecosystem norms | — Pending |
| Port 9090 default | Doesn't conflict with OpenClaw (3000) or health (8080) | — Pending |

---
*Last updated: 2026-02-23 after initialization*

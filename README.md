# openclaw-trace

OpenClaw Trace is a lightweight Go sidecar for agent observability.

## Phase 1 Quickstart

```bash
export OCT_PORT=9090
export OCT_DB_PATH=/tmp/openclaw-trace.db
export OCT_LOG_LEVEL=info
export OCT_PUSH_ENDPOINT=
export OCT_PUSH_INTERVAL=5m
export OCT_RETENTION_DAYS=3
export OCT_MAX_TEXT_BYTES=16384

go run ./cmd/openclaw-trace
```

In another shell:

```bash
curl -s http://127.0.0.1:9090/health
```

## Build Verification

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
```

## Hardening Checks

```bash
./scripts/check-binary-size.sh
go test ./...
```

GoReleaser snapshot configuration is available in `.goreleaser.yaml`.

## Release + VPS Rollout

See `docs/PUBLISH_AND_AUGMI_VPS_ROLLOUT.md`.

## Graceful Shutdown Check

Start the service, then send SIGTERM:

```bash
pkill -f openclaw-trace
```

The process should log:
- `SIGTERM received, shutting down...`
- ingest drain message
- final push status
- shutdown complete with uptime/event totals

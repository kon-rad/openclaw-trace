# Publish And Augmi VPS Rollout

## 1. Publish Release

From `openclaw-trace/`:

```bash
./scripts/release-publish.sh v0.1.0
```

This runs:
- `go test ./...`
- binary size gate (`< 15MB`, linux/amd64 stripped)
- git tag push
- `goreleaser release --clean`

## 2. Install On Augmi VPS

On each VPS:

```bash
curl -fsSL https://raw.githubusercontent.com/<ORG>/<REPO>/main/deploy/vps/install-openclaw-trace.sh -o /tmp/install-openclaw-trace.sh
chmod +x /tmp/install-openclaw-trace.sh
/tmp/install-openclaw-trace.sh v0.1.0 <ORG>/<REPO>
```

## 3. Configure Runtime

```bash
sudo mkdir -p /etc/openclaw-trace
sudo cp deploy/vps/openclaw-trace.env.example /etc/openclaw-trace/openclaw-trace.env
sudo nano /etc/openclaw-trace/openclaw-trace.env
```

Set at minimum:
- `OCT_PUSH_ENDPOINT` to Augmi ingest endpoint
- `OCT_DB_PATH` to persistent volume path
- `OCT_LOG_PATH` to OpenClaw gateway log path (optional parsing mode)

## 4A. Start Via systemd (recommended)

```bash
sudo cp deploy/vps/openclaw-trace.service /etc/systemd/system/openclaw-trace.service
sudo systemctl daemon-reload
sudo systemctl enable openclaw-trace
sudo systemctl restart openclaw-trace
sudo systemctl status openclaw-trace --no-pager
```

## 4B. Start Via OpenClaw start.sh (sidecar mode)

Use `deploy/vps/start.sh.snippet` in your OpenClaw bootstrap script.

## 5. Validate On VPS

```bash
curl -s http://127.0.0.1:9090/health
tail -n 100 /var/log/openclaw-trace/openclaw-trace.log
```

Health response should include:
- `db_status: "ok"`
- nonzero `events_received` after traffic
- `last_push_status: "ok"` once push is successful

## 6. Rollout Checklist

- [ ] Release tag published
- [ ] Binary installed on each VPS
- [ ] Env configured per instance
- [ ] Service enabled and healthy
- [ ] OpenClaw agent sending traces to `http://127.0.0.1:9090/v1/traces`
- [ ] Push endpoint receiving events

#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT}/.cache/hardening"
BIN_PATH="${OUT_DIR}/openclaw-trace-linux-amd64"
MAX_BYTES=$((15 * 1024 * 1024))

mkdir -p "${OUT_DIR}"

echo "[hardening] building linux/amd64 stripped binary..."
GOCACHE="${ROOT}/.cache/go-build" \
GOMODCACHE="${ROOT}/.cache/go-mod" \
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
go build -trimpath -ldflags="-s -w" -o "${BIN_PATH}" ./cmd/openclaw-trace

SIZE="$(wc -c < "${BIN_PATH}" | tr -d '[:space:]')"
echo "[hardening] binary size bytes: ${SIZE}"

if [[ "${SIZE}" -gt "${MAX_BYTES}" ]]; then
  echo "[hardening] FAIL: binary exceeds 15MB limit"
  exit 1
fi

echo "[hardening] PASS: binary within size budget"

#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 <version-tag> <repo-slug>"
  echo "example: $0 v0.1.0 kon-rad/openclaw-trace"
  exit 1
fi

VERSION="$1"
REPO="$2"

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64) ARCH_TAG="amd64" ;;
  aarch64|arm64) ARCH_TAG="arm64" ;;
  *)
    echo "unsupported architecture: ${ARCH}"
    exit 1
    ;;
esac

TMP_DIR="$(mktemp -d)"
TARBALL="openclaw-trace_${VERSION#v}_linux_${ARCH_TAG}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${TARBALL}"

echo "[install] downloading ${URL}"
curl -fL "${URL}" -o "${TMP_DIR}/${TARBALL}"

echo "[install] extracting"
tar -xzf "${TMP_DIR}/${TARBALL}" -C "${TMP_DIR}"

if [[ ! -f "${TMP_DIR}/openclaw-trace" ]]; then
  echo "binary not found in release archive"
  exit 1
fi

echo "[install] installing binary to /usr/local/bin/openclaw-trace"
sudo install -m 0755 "${TMP_DIR}/openclaw-trace" /usr/local/bin/openclaw-trace

echo "[install] ensuring runtime directories"
sudo mkdir -p /etc/openclaw-trace /var/lib/openclaw-trace /var/log/openclaw-trace
sudo chown -R "$(id -u):$(id -g)" /var/lib/openclaw-trace /var/log/openclaw-trace

echo "[install] done"

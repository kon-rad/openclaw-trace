#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <version-tag>"
  echo "example: $0 v0.1.0"
  exit 1
fi

VERSION="$1"

if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must match vMAJOR.MINOR.PATCH (got: ${VERSION})"
  exit 1
fi

echo "[release] running tests..."
GOCACHE="${ROOT}/.cache/go-build" GOMODCACHE="${ROOT}/.cache/go-mod" go test ./...

echo "[release] checking binary size budget..."
"${ROOT}/scripts/check-binary-size.sh"

if ! command -v goreleaser >/dev/null 2>&1; then
  echo "[release] goreleaser not found. Install: https://goreleaser.com/install/"
  exit 1
fi

echo "[release] creating git tag ${VERSION}..."
git tag -a "${VERSION}" -m "release: ${VERSION}"

echo "[release] pushing tag..."
git push origin "${VERSION}"

echo "[release] publishing GitHub release via goreleaser..."
goreleaser release --clean

echo "[release] done"

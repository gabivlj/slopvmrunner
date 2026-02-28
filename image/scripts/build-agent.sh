#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
OUT_DIR="$REPO_ROOT/build"
VERBOSE="${VERBOSE:-0}"

if [[ "$VERBOSE" == "1" ]]; then
  set -x
fi

log() {
  echo "[build-agent] $*"
}

source "$SCRIPT_DIR/lib/arch.sh"

GOARCH="$(to_goarch)"

mkdir -p "$OUT_DIR"

log "building guest agent for linux/$GOARCH"
pushd "$REPO_ROOT/agent" >/dev/null
CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath -ldflags='-s -w' -o "$OUT_DIR/agent" ./cmd/agent
popd >/dev/null

echo "built $OUT_DIR/agent (linux/$GOARCH)"

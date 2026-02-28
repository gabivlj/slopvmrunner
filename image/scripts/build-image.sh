#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERBOSE="${VERBOSE:-0}"

if [[ "$VERBOSE" == "1" ]]; then
  set -x
fi

log() {
  echo "[build-image] $*"
}

log "starting full image build pipeline"
"$SCRIPT_DIR/build-agent.sh"
"$SCRIPT_DIR/build-rootfs.sh"
"$SCRIPT_DIR/build-kernel.sh"
"$SCRIPT_DIR/make-raw-image.sh"
log "pipeline complete"

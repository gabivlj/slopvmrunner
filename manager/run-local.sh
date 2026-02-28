#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ $# -lt 1 ]]; then
  cat >&2 <<'USAGE'
Usage: ./run-local.sh --kernel <path> --root-image <path> [other vmmanager args]
USAGE
  exit 1
fi

pushd "$SCRIPT_DIR" >/dev/null
swift build -c debug
codesign --force --sign - --entitlements "$SCRIPT_DIR/vmmanager.entitlements" .build/debug/vmmanager
./.build/debug/vmmanager "$@"
popd >/dev/null

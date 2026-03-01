#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_PATH="$SCRIPT_DIR/../build/vmmanager"
BUILD_ONLY=0

usage() {
  cat >&2 <<'USAGE'
Usage: ./run-local.sh [--out <path>] [--build-only] [-- <vmmanager args>]
USAGE
}

RUN_ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out)
      shift
      [[ $# -gt 0 ]] || { usage; exit 1; }
      OUT_PATH="$1"
      ;;
    --build-only)
      BUILD_ONLY=1
      ;;
    --)
      shift
      RUN_ARGS+=("$@")
      break
      ;;
    *)
      RUN_ARGS+=("$1")
      ;;
  esac
  shift
done

pushd "$SCRIPT_DIR" >/dev/null
swift build -c debug
mkdir -p "$(dirname "$OUT_PATH")"
cp .build/debug/vmmanager "$OUT_PATH"
codesign --force --sign - --entitlements "$SCRIPT_DIR/vmmanager.entitlements" "$OUT_PATH"
if [[ "$BUILD_ONLY" -eq 0 && "${#RUN_ARGS[@]}" -gt 0 ]]; then
  "$OUT_PATH" "${RUN_ARGS[@]}"
fi
popd >/dev/null

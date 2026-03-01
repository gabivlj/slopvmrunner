#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_PATH="$SCRIPT_DIR/../build/vmmanager"
BUILD_ONLY=0
ENV_FILE="$SCRIPT_DIR/.env.local"
SIGN_IDENTITY="${CODESIGN_IDENTITY:-}"
ENTITLEMENTS_PATH="${VMMANAGER_ENTITLEMENTS:-$SCRIPT_DIR/vmmanager.entitlements}"
VERBOSE="${VERBOSE:-1}"

log() {
  if [[ "$VERBOSE" == "1" ]]; then
    echo "[run-local] $*" >&2
  fi
}

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

if [[ -f "$ENV_FILE" ]]; then
  log "loading env file: $ENV_FILE"
  # shellcheck disable=SC1090
  source "$ENV_FILE"
else
  log "env file not found: $ENV_FILE"
fi

if [[ -z "${SIGN_IDENTITY}" ]]; then
  SIGN_IDENTITY="${CODESIGN_IDENTITY:-}"
fi
if [[ -z "${SIGN_IDENTITY}" ]]; then
  SIGN_IDENTITY="-"
fi

log "script_dir=$SCRIPT_DIR"
log "out_path=$OUT_PATH"
log "build_only=$BUILD_ONLY"
log "sign_identity=$SIGN_IDENTITY"
log "entitlements_path=$ENTITLEMENTS_PATH"
log "run_args_count=${#RUN_ARGS[@]}"

pushd "$SCRIPT_DIR" >/dev/null
log "swift build -c debug"
swift build -c debug
mkdir -p "$(dirname "$OUT_PATH")"
cp .build/debug/vmmanager "$OUT_PATH"
log "codesign vmmanager with entitlements: $ENTITLEMENTS_PATH"
codesign --force --sign "$SIGN_IDENTITY" --entitlements "$ENTITLEMENTS_PATH" "$OUT_PATH"
log "codesign complete: $OUT_PATH"
if [[ "$BUILD_ONLY" -eq 0 && "${#RUN_ARGS[@]}" -gt 0 ]]; then
  log "executing vmmanager with forwarded args"
  "$OUT_PATH" "${RUN_ARGS[@]}"
fi
popd >/dev/null

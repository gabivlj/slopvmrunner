#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
API_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="$API_DIR/gen/go"

CAPNPC_GO="${CAPNPC_GO:-$(command -v capnpc-go || true)}"
if [[ -z "${CAPNPC_GO}" ]]; then
  echo "capnpc-go not found in PATH; install with:" >&2
  echo "  go install capnproto.org/go/capnp/v3/capnpc-go@latest" >&2
  exit 1
fi

CAPNP_MOD_DIR=""
DOWNLOAD_JSON="$(cd "$API_DIR" && go mod download -json capnproto.org/go/capnp/v3 2>/dev/null || true)"
if [[ -n "$DOWNLOAD_JSON" ]]; then
  CAPNP_MOD_DIR="$(printf '%s\n' "$DOWNLOAD_JSON" | sed -n 's/^[[:space:]]*"Dir":[[:space:]]*"\(.*\)",$/\1/p' | head -n1)"
fi

if [[ -z "$CAPNP_MOD_DIR" || ! -d "$CAPNP_MOD_DIR" ]]; then
  GOMODCACHE="$(cd "$API_DIR" && go env GOMODCACHE)"
  for d in "$GOMODCACHE"/capnproto.org/go/capnp/v3@*; do
    if [[ -d "$d/std" ]]; then
      CAPNP_MOD_DIR="$d"
      break
    fi
  done
fi

STD_DIR="$CAPNP_MOD_DIR/std"
if [[ -z "$CAPNP_MOD_DIR" || ! -d "$STD_DIR" ]]; then
  echo "failed to resolve capnp std includes (expected $STD_DIR)" >&2
  echo "try: cd api && go mod download capnproto.org/go/capnp/v3" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

pushd "$API_DIR" >/dev/null
capnp compile \
  -I"capnp" \
  -I"$STD_DIR" \
  -o"$CAPNPC_GO:gen/go" \
  "capnp/agent.capnp"
popd >/dev/null

echo "generated Go bindings in $OUT_DIR"

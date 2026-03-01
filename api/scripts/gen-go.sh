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

CAPNP_MOD_DIR="$(cd "$API_DIR" && go list -f '{{.Dir}}' -m capnproto.org/go/capnp/v3)"
STD_DIR="$CAPNP_MOD_DIR/std"

mkdir -p "$OUT_DIR"

pushd "$API_DIR" >/dev/null
capnp compile \
  -I"capnp" \
  -I"$STD_DIR" \
  -o"$CAPNPC_GO:gen/go" \
  "capnp/agent.capnp"
popd >/dev/null

echo "generated Go bindings in $OUT_DIR"

#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_DIR="${BUILD_DIR:-$REPO_ROOT/build}"
ROOTFS_DIR="${ROOTFS_DIR:-$BUILD_DIR/rootfs-tree}"
IMG_PATH="${1:-$BUILD_DIR/rootfs.raw}"
SIZE_MB="${2:-1024}"
VERBOSE="${VERBOSE:-0}"

if [[ "$VERBOSE" == "1" ]]; then
  set -x
fi

log() {
  echo "[make-raw-image] $*"
}

if [[ ! -d "$ROOTFS_DIR" ]]; then
  echo "missing rootfs at $ROOTFS_DIR. run build-rootfs.sh first" >&2
  exit 1
fi

make_image_with_host_tools() {
  log "creating ext4 image with host mkfs.ext4"
  mkdir -p "$(dirname "$IMG_PATH")"
  rm -f "$IMG_PATH"
  truncate -s "${SIZE_MB}M" "$IMG_PATH"
  # -d copies full directory tree (files, symlinks, perms) into ext4 image.
  mkfs.ext4 -F -d "$ROOTFS_DIR" "$IMG_PATH" >/dev/null
  echo "raw image created at $IMG_PATH"
}

make_image_with_docker() {
  log "creating ext4 image with Docker fallback"
  local rel_img
  if [[ "$IMG_PATH" == "$BUILD_DIR/"* ]]; then
    rel_img="${IMG_PATH#"$BUILD_DIR"/}"
  else
    rel_img="$(basename "$IMG_PATH")"
  fi
  local work_img="/work/build/${rel_img}"
  local work_rootfs="/work/build/rootfs-tree"

  docker run --rm \
    -v "$BUILD_DIR:/work/build" \
    -w /work \
    -e IMG_PATH="$work_img" \
    -e ROOTFS_DIR="$work_rootfs" \
    -e SIZE_MB="$SIZE_MB" \
    ubuntu:24.04 \
    bash -lc '
      set -euo pipefail
      export DEBIAN_FRONTEND=noninteractive
      apt-get update >/dev/null
      apt-get install -y --no-install-recommends e2fsprogs >/dev/null

      mkdir -p "$(dirname "$IMG_PATH")"
      rm -f "$IMG_PATH"
      truncate -s "${SIZE_MB}M" "$IMG_PATH"
      mke2fs -t ext4 -F -d "$ROOTFS_DIR" "$IMG_PATH" >/dev/null
    '

  echo "raw image created at $IMG_PATH (via Docker)"
}

if command -v mkfs.ext4 >/dev/null 2>&1; then
  make_image_with_host_tools
  exit 0
fi

if command -v docker >/dev/null 2>&1; then
  make_image_with_docker
  exit 0
fi

cat >&2 <<'MSG'
missing required tool: mkfs.ext4 (from e2fsprogs).
install those locally, or install/start Docker so this script can use a Linux container fallback.
MSG
exit 1

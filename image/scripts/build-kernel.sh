#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_DIR="$REPO_ROOT/build"
ROOTFS_DIR="$BUILD_DIR/rootfs"
VERBOSE="${VERBOSE:-0}"

if [[ "$VERBOSE" == "1" ]]; then
  set -x
fi

log() {
  echo "[build-kernel] $*"
}

source "$SCRIPT_DIR/lib/arch.sh"

ALPINE_ARCH="$(to_alpine_arch)"
ALPINE_VERSION="$(cat "$BUILD_DIR/alpine.version" 2>/dev/null || echo "3.20.3")"
ALPINE_BRANCH="v$(echo "$ALPINE_VERSION" | cut -d. -f1,2)"
KERNEL_MODE="${KERNEL_MODE:-auto}" # auto|package|source
KERNEL_BACKUP_CREATED=0
KERNEL_BUILD_OK=0

if [[ ! -d "$ROOTFS_DIR" ]]; then
  echo "missing rootfs at $ROOTFS_DIR. run build-rootfs.sh first" >&2
  exit 1
fi

if [[ ! -f "$BUILD_DIR/rootfs.arch" ]]; then
  echo "missing $BUILD_DIR/rootfs.arch. rerun build-rootfs.sh" >&2
  exit 1
fi
ROOTFS_ARCH="$(cat "$BUILD_DIR/rootfs.arch")"
if [[ "$ROOTFS_ARCH" != "$ALPINE_ARCH" ]]; then
  echo "rootfs arch ($ROOTFS_ARCH) does not match host arch ($ALPINE_ARCH)" >&2
  echo "rerun build-rootfs.sh to regenerate rootfs for this host" >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found. install/start Docker to build kernel artifacts" >&2
  exit 1
fi

kernel_is_valid() {
  if [[ "$(uname -m)" == "arm64" ]]; then
    file "$BUILD_DIR/kernel" | grep -q "Linux kernel ARM64 boot executable Image"
  else
    [[ -s "$BUILD_DIR/kernel" ]]
  fi
}

build_from_packages() {
  log "trying package kernel path (alpine linux-virt)"
  docker run --rm \
    -v "$REPO_ROOT:/work" \
    -w /work \
    alpine:3.20 \
    sh -euc '
      REPO_MAIN="https://dl-cdn.alpinelinux.org/alpine/'"$ALPINE_BRANCH"'/main"
      REPO_COMMUNITY="https://dl-cdn.alpinelinux.org/alpine/'"$ALPINE_BRANCH"'/community"

      apk add --no-cache --repository "$REPO_MAIN" --repository "$REPO_COMMUNITY" \
        linux-virt nftables iproute2 ethtool e2fsprogs btrfs-progs xfsprogs file

      cp /boot/vmlinuz-virt /work/build/vmlinuz-virt
      cp /boot/vmlinuz-virt /work/build/kernel

      rm -rf /work/build/rootfs/lib/modules
      mkdir -p /work/build/rootfs/lib
      cp -a /lib/modules /work/build/rootfs/lib/

      mkdir -p /work/build/rootfs/etc/apk
      printf "%s\n%s\n" "$REPO_MAIN" "$REPO_COMMUNITY" > /work/build/rootfs/etc/apk/repositories

      apk --root /work/build/rootfs --keys-dir /etc/apk/keys --repositories-file /work/build/rootfs/etc/apk/repositories \
        add --no-cache --no-scripts nftables iproute2 ethtool e2fsprogs btrfs-progs xfsprogs
    '
}

run_source_fallback() {
  log "running source kernel fallback"
  "$SCRIPT_DIR/build-kernel-source.sh"
}

case "$KERNEL_MODE" in
  package)
    build_from_packages
    ;;
  source)
    run_source_fallback
    ;;
  auto)
    if build_from_packages && kernel_is_valid; then
      log "package kernel is valid for this host"
      :
    else
      echo "packaged kernel is not a valid Linux Image for this host; falling back to source build" >&2
      run_source_fallback
    fi
    ;;
  *)
    echo "invalid KERNEL_MODE: $KERNEL_MODE (expected auto|package|source)" >&2
    exit 1
    ;;
esac

if ! kernel_is_valid; then
  echo "kernel artifact at $BUILD_DIR/kernel is not an uncompressed ARM64 Image" >&2
  echo "detected: $(file "$BUILD_DIR/kernel")" >&2
  echo "set KERNEL_MODE=source and rerun build-kernel.sh" >&2
  exit 1
fi

ln -sf "$(basename "$BUILD_DIR/kernel")" "$BUILD_DIR/vmlinuz"
KERNEL_BUILD_OK=1

echo "kernel ready at $BUILD_DIR/kernel (symlinked as $BUILD_DIR/vmlinuz)"
echo "modules and fs/network tooling staged into $ROOTFS_DIR"

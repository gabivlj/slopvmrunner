#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_DIR="$REPO_ROOT/build"
ROOTFS_DIR="$BUILD_DIR/rootfs"
VERBOSE="${VERBOSE:-0}"

if [[ "$VERBOSE" == "1" ]]; then
  set -x
fi

log() {
  echo "[build-kernel-source] $*"
}

source "$SCRIPT_DIR/lib/arch.sh"

if [[ "$(to_alpine_arch)" != "aarch64" ]]; then
  echo "build-kernel-source.sh currently supports arm64 hosts only" >&2
  exit 1
fi

if [[ ! -d "$ROOTFS_DIR" ]]; then
  echo "missing rootfs at $ROOTFS_DIR. run build-rootfs.sh first" >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found. install/start Docker to build kernel artifacts" >&2
  exit 1
fi

KERNEL_VERSION="${KERNEL_VERSION:-6.6.80}"
KERNEL_TARBALL="linux-${KERNEL_VERSION}.tar.xz"
KERNEL_URL="https://cdn.kernel.org/pub/linux/kernel/v6.x/${KERNEL_TARBALL}"
JOBS="${KERNEL_JOBS:-$(sysctl -n hw.ncpu 2>/dev/null || echo 4)}"

mkdir -p "$BUILD_DIR"

log "building Linux $KERNEL_VERSION from source for arm64"
docker run --rm \
  -v "$REPO_ROOT:/work" \
  -w /work \
  debian:bookworm \
  bash -euc '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update >/dev/null
    apt-get install -y --no-install-recommends \
      ca-certificates curl xz-utils make gcc bc bison flex libssl-dev libelf-dev rsync kmod >/dev/null

    mkdir -p /work/build/cache
    if [[ ! -f "/work/build/cache/'"$KERNEL_TARBALL"'" ]]; then
      curl -L "'"$KERNEL_URL"'" -o "/work/build/cache/'"$KERNEL_TARBALL"'"
    fi

    rm -rf /tmp/linux-src
    mkdir -p /tmp/linux-src
    tar -xf "/work/build/cache/'"$KERNEL_TARBALL"'" -C /tmp/linux-src --strip-components=1
    cd /tmp/linux-src

    make ARCH=arm64 defconfig >/dev/null

    scripts/config --disable DEBUG_INFO \
                   --disable DEBUG_INFO_BTF \
                   --enable DEVTMPFS \
                   --enable DEVTMPFS_MOUNT \
                   --enable VIRTIO \
                   --enable VIRTIO_MMIO \
                   --enable VIRTIO_BLK \
                   --enable VIRTIO_NET \
                   --enable VIRTIO_CONSOLE \
                   --enable EXT4_FS \
                   --enable TMPFS \
                   --enable CGROUPS \
                   --enable NAMESPACES \
                   --enable NF_TABLES \
                   --enable OVERLAY_FS

    make ARCH=arm64 olddefconfig >/dev/null
    make -j"'"$JOBS"'" ARCH=arm64 Image modules >/dev/null

    cp arch/arm64/boot/Image /work/build/kernel

    rm -rf /work/build/rootfs/lib/modules
    make -j"'"$JOBS"'" ARCH=arm64 modules_install INSTALL_MOD_PATH=/work/build/rootfs >/dev/null
  '

# Rootfs is Alpine: install runtime fs/network tools with apk in an Alpine container.
ALPINE_VERSION="$(cat "$BUILD_DIR/alpine.version" 2>/dev/null || echo "3.20.3")"
ALPINE_BRANCH="v$(echo "$ALPINE_VERSION" | cut -d. -f1,2)"

log "installing fs/network tools into rootfs"
docker run --rm \
  -v "$REPO_ROOT:/work" \
  -w /work \
  alpine:3.20 \
  sh -euc '
    REPO_MAIN="https://dl-cdn.alpinelinux.org/alpine/'"$ALPINE_BRANCH"'/main"
    REPO_COMMUNITY="https://dl-cdn.alpinelinux.org/alpine/'"$ALPINE_BRANCH"'/community"

    mkdir -p /work/build/rootfs/etc/apk
    printf "%s\n%s\n" "$REPO_MAIN" "$REPO_COMMUNITY" > /work/build/rootfs/etc/apk/repositories

    apk --root /work/build/rootfs --keys-dir /etc/apk/keys --repositories-file /work/build/rootfs/etc/apk/repositories \
      add --no-cache --no-scripts nftables iproute2 ethtool e2fsprogs btrfs-progs xfsprogs
  '

ln -sf "$(basename "$BUILD_DIR/kernel")" "$BUILD_DIR/vmlinuz"

echo "source-built kernel ready at $BUILD_DIR/kernel"

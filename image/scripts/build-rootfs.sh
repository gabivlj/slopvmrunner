#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_DIR="${BUILD_DIR:-$REPO_ROOT/build}"
ROOTFS_DIR="${ROOTFS_DIR:-$BUILD_DIR/rootfs-tree}"
OVERLAY_DIR="$REPO_ROOT/image/rootfs-overlay"
ALPINE_VERSION="${ALPINE_VERSION:-3.20.3}"
RUNC_VERSION="${RUNC_VERSION:-1.2.6}"
VERBOSE="${VERBOSE:-0}"

if [[ "$VERBOSE" == "1" ]]; then
  set -x
fi

log() {
  echo "[build-rootfs] $*"
}

source "$SCRIPT_DIR/lib/arch.sh"

ALPINE_ARCH="$(to_alpine_arch)"
RUNC_ARCH="$(to_runc_arch)"
ALPINE_BRANCH="v$(echo "$ALPINE_VERSION" | cut -d. -f1,2)"
ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/releases/${ALPINE_ARCH}/alpine-minirootfs-${ALPINE_VERSION}-${ALPINE_ARCH}.tar.gz"

mkdir -p "$BUILD_DIR"
rm -rf "$ROOTFS_DIR"
mkdir -p "$ROOTFS_DIR"

TARBALL_PATH="$BUILD_DIR/alpine-minirootfs-${ALPINE_VERSION}-${ALPINE_ARCH}.tar.gz"

if [[ ! -f "$TARBALL_PATH" ]]; then
  log "downloading alpine minirootfs: $ALPINE_URL"
  curl -L "$ALPINE_URL" -o "$TARBALL_PATH"
fi

log "extracting rootfs tarball into $ROOTFS_DIR"
tar -xzf "$TARBALL_PATH" -C "$ROOTFS_DIR"

log "applying rootfs overlay and installing agent as /sbin/agent"
cp -R "$OVERLAY_DIR"/* "$ROOTFS_DIR"/
install -m 0755 "$BUILD_DIR/agent" "$ROOTFS_DIR/sbin/agent"

# Mountpoint for writable container state disk when guest root disk is read-only.
mkdir -p "$ROOTFS_DIR/containers"

# Mountpoint for virtiofs-shared image store.
# Alpine usually links /var/run -> /run, so create /run directly.
mkdir -p "$ROOTFS_DIR/run/vmrunner"

RUNC_BIN_PATH="$BUILD_DIR/runc.${RUNC_ARCH}"
if [[ ! -f "$RUNC_BIN_PATH" ]]; then
  RUNC_URL="https://github.com/opencontainers/runc/releases/download/v${RUNC_VERSION}/runc.${RUNC_ARCH}"
  log "downloading runc binary: $RUNC_URL"
  curl -L "$RUNC_URL" -o "$RUNC_BIN_PATH"
fi
install -m 0755 "$RUNC_BIN_PATH" "$ROOTFS_DIR/usr/bin/runc"

# Keep pid1 simple and deterministic: agent is init.
ln -sf /sbin/agent "$ROOTFS_DIR/sbin/init"

# Basic defaults for predictable serial/console behavior.
cat > "$ROOTFS_DIR/etc/inittab" <<'INITTAB'
::sysinit:/bin/mount -t proc proc /proc
::sysinit:/bin/mount -t sysfs sysfs /sys
::respawn:/bin/sh
::ctrlaltdel:/bin/umount -a -r
INITTAB

cat > "$ROOTFS_DIR/etc/resolv.conf" <<'RESOLV'
nameserver 1.1.1.1
nameserver 8.8.8.8
RESOLV

echo "$ALPINE_ARCH" > "$BUILD_DIR/rootfs.arch"
echo "$ALPINE_VERSION" > "$BUILD_DIR/alpine.version"

echo "rootfs assembled at $ROOTFS_DIR (alpine $ALPINE_VERSION/$ALPINE_ARCH)"

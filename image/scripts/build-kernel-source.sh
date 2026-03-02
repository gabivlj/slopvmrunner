#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_DIR="${BUILD_DIR:-$REPO_ROOT/build}"
ROOTFS_DIR="${ROOTFS_DIR:-$BUILD_DIR/rootfs-tree}"
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
JOBS="${KERNEL_JOBS:-2}"
KERNEL_BUILD_LOG="$BUILD_DIR/kernel-build.log"

mkdir -p "$BUILD_DIR"

log "building Linux $KERNEL_VERSION from source for arm64"
log "using parallel jobs: $JOBS (override with KERNEL_JOBS)"
docker run --rm \
  -v "$BUILD_DIR:/work/build" \
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

    make ARCH=arm64 defconfig

    scripts/config --disable DEBUG_INFO \
                   --disable DEBUG_INFO_BTF \
                   --disable AGP \
                   --disable DRM \
                   --disable DRM_KMS_HELPER \
                   --disable DRM_TTM \
                   --disable TTM \
                   --disable DRM_NOUVEAU \
                   --disable NOUVEAU_DEBUG \
                   --disable DRM_AMDGPU \
                   --disable DRM_RADEON \
                   --disable DRM_I915 \
                   --disable DRM_MSM \
                   --disable DRM_VC4 \
                   --disable DRM_V3D \
                   --disable DRM_PANFROST \
                   --disable DRM_LIMA \
                   --disable DRM_TEGRA \
                   --disable DRM_ETNAVIV \
                   --disable FB \
                   --disable FRAMEBUFFER_CONSOLE \
                   --disable VGA_CONSOLE \
                   --disable USB_SUPPORT \
                   --enable DEVTMPFS \
                   --enable DEVTMPFS_MOUNT \
                   --enable VSOCKETS \
                   --enable VIRTIO_VSOCKETS \
                   --enable VIRTIO_VSOCKETS_COMMON \
                   --enable VIRTIO \
                   --enable VIRTIO_MMIO \
                   --enable VIRTIO_BLK \
                   --enable VIRTIO_FS \
                   --enable VIRTIO_NET \
                   --enable VIRTIO_CONSOLE \
                   --enable NET \
                   --enable PACKET \
                   --enable UNIX \
                   --enable INET \
                   --enable IPV6 \
                   --enable NETFILTER \
                   --enable NETFILTER_ADVANCED \
                   --enable NETFILTER_INGRESS \
                   --enable NF_CONNTRACK \
                   --enable NF_CONNTRACK_EVENTS \
                   --enable NF_CONNTRACK_LABELS \
                   --enable NF_NAT \
                   --enable NF_NAT_REDIRECT \
                   --enable NETFILTER_XTABLES \
                   --enable NETFILTER_XT_MATCH_CONNTRACK \
                   --enable NETFILTER_XT_MATCH_MARK \
                   --enable NETFILTER_XT_MATCH_ADDRTYPE \
                   --enable NETFILTER_XT_MATCH_SOCKET \
                   --enable NETFILTER_XT_TARGET_MARK \
                   --enable NETFILTER_XT_TARGET_REDIRECT \
                   --enable NETFILTER_XT_TARGET_TPROXY \
                   --enable NETFILTER_XT_TARGET_CT \
                   --enable NETFILTER_XT_TARGET_MASQUERADE \
                   --enable IP_NF_IPTABLES \
                   --enable IP_NF_FILTER \
                   --enable IP_NF_NAT \
                   --enable IP_NF_MANGLE \
                   --enable IP_NF_RAW \
                   --enable IP6_NF_IPTABLES \
                   --enable IP6_NF_FILTER \
                   --enable IP6_NF_NAT \
                   --enable IP6_NF_MANGLE \
                   --enable IP6_NF_RAW \
                   --enable NF_TABLES \
                   --enable NF_TABLES_INET \
                   --enable NF_TABLES_IPV4 \
                   --enable NF_TABLES_IPV6 \
                   --enable NF_TABLES_NETDEV \
                   --enable NFT_CT \
                   --enable NFT_FIB \
                   --enable NFT_LOG \
                   --enable NFT_LIMIT \
                   --enable NFT_REJECT \
                   --enable NFT_REJECT_INET \
                   --enable NFT_NAT \
                   --enable NFT_MASQ \
                   --enable NFT_REDIR \
                   --enable NFT_SOCKET \
                   --enable NFT_TPROXY \
                   --enable NF_TPROXY_IPV4 \
                   --enable NF_TPROXY_IPV6 \
                   --enable IP_ADVANCED_ROUTER \
                   --enable IP_MULTIPLE_TABLES \
                   --enable IPV6_MULTIPLE_TABLES \
                   --enable IP_ROUTE_MULTIPATH \
                   --enable IP_ROUTE_VERBOSE \
                   --enable IP_TRANSPARENT \
                   --enable NET_CLS_ROUTE4 \
                   --enable NET_SCH_INGRESS \
                   --enable NET_SCH_HTB \
                   --enable NET_SCH_FQ \
                   --enable NET_SCH_FQ_CODEL \
                   --enable NET_SCH_CAKE \
                   --enable NET_CLS_U32 \
                   --enable NET_CLS_BPF \
                   --enable NET_CLS_ACT \
                   --enable NET_ACT_MIRRED \
                   --enable NET_ACT_POLICE \
                   --enable NET_ACT_CT \
                   --enable NET_ACT_TUNNEL_KEY \
                   --enable BPF \
                   --enable BPF_SYSCALL \
                   --enable BPF_JIT \
                   --enable CGROUP_BPF \
                   --enable VETH \
                   --enable TUN \
                   --enable BRIDGE \
                   --enable BRIDGE_NETFILTER \
                   --enable VLAN_8021Q \
                   --enable VXLAN \
                   --enable GENEVE \
                   --enable MACVLAN \
                   --enable DUMMY \
                   --enable IFB \
                   --enable XFRM \
                   --enable XFRM_USER \
                   --enable XFRM_ALGO \
                   --enable INET_ESP \
                   --enable INET6_ESP \
                   --enable EXT4_FS \
                   --enable FUSE_FS \
                   --enable TMPFS \
                   --enable CGROUPS \
                   --enable NAMESPACES \
                   --enable NF_TABLES \
                   --enable OVERLAY_FS

    make ARCH=arm64 olddefconfig
    set -o pipefail
    if ! make -j"'"$JOBS"'" ARCH=arm64 V=1 Image modules 2>&1 | tee /work/build/kernel-build.log; then
      echo "kernel build failed; showing last 200 log lines" >&2
      tail -n 200 /work/build/kernel-build.log >&2
      if grep -q "Killed signal terminated program cc1" /work/build/kernel-build.log; then
        echo "detected likely OOM during compile (cc1 killed)." >&2
        echo "retry with lower parallelism: KERNEL_JOBS=1 make kernel" >&2
        echo "also increase Docker memory if possible." >&2
      fi
      exit 1
    fi

    cp arch/arm64/boot/Image /work/build/kernel

    rm -rf /work/build/rootfs-tree/lib/modules
    if ! make -j"'"$JOBS"'" ARCH=arm64 V=1 modules_install INSTALL_MOD_PATH=/work/build/rootfs-tree 2>&1 | tee -a /work/build/kernel-build.log; then
      echo "modules_install failed; showing last 200 log lines" >&2
      tail -n 200 /work/build/kernel-build.log >&2
      exit 1
    fi
  '

# Rootfs is Alpine: install runtime fs/network tools with apk in an Alpine container.
ALPINE_VERSION="$(cat "$BUILD_DIR/alpine.version" 2>/dev/null || echo "3.20.3")"
ALPINE_BRANCH="v$(echo "$ALPINE_VERSION" | cut -d. -f1,2)"

log "installing fs/network tools into rootfs"
docker run --rm \
  -v "$BUILD_DIR:/work/build" \
  -w /work \
  alpine:3.20 \
  sh -euc '
    REPO_MAIN="https://dl-cdn.alpinelinux.org/alpine/'"$ALPINE_BRANCH"'/main"
    REPO_COMMUNITY="https://dl-cdn.alpinelinux.org/alpine/'"$ALPINE_BRANCH"'/community"

    mkdir -p /work/build/rootfs-tree/etc/apk
    printf "%s\n%s\n" "$REPO_MAIN" "$REPO_COMMUNITY" > /work/build/rootfs-tree/etc/apk/repositories

    apk --root /work/build/rootfs-tree --keys-dir /etc/apk/keys --repositories-file /work/build/rootfs-tree/etc/apk/repositories \
      add --no-cache --no-scripts nftables iproute2 ethtool e2fsprogs btrfs-progs xfsprogs
  '

ln -sf "$(basename "$BUILD_DIR/kernel")" "$BUILD_DIR/vmlinuz"

echo "source-built kernel ready at $BUILD_DIR/kernel"
echo "kernel build log at $KERNEL_BUILD_LOG"

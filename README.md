# vmrunner

Fast-boot Linux microVM experiments on macOS ("firecracker for Macs") using Apple Virtualization.

## Components

- `manager/`: macOS VM manager (Swift + `Virtualization` framework)
- `agent/`: Linux guest agent (Go), currently intended to run as `pid 1`
- `image/`: scripts to build a minimal raw root disk image

## Current Status

This repo is now bootstrapped with:

- a manager CLI that builds a `VZVirtualMachineConfiguration`
- a guest agent that listens on TCP and returns `hello world`
- image scripts that assemble a raw ext4 root image and set `/sbin/init -> /sbin/agent`
- a reproducible kernel pipeline (`linux-virt`) with modules/tools copied into the rootfs

## Task Breakdown

### 1. Base raw image (in progress)

Goal: build a simple, useful Linux root image where we control `pid 1`.

Implemented now:

1. Build static Linux agent binary:
   - `image/scripts/build-agent.sh`
2. Assemble rootfs from Alpine minirootfs + overlay:
   - `image/scripts/build-rootfs.sh`
3. Create raw ext4 image from rootfs:
   - `image/scripts/make-raw-image.sh`

One-shot helper:

```bash
./image/scripts/build-image.sh
```

Verbose run:

```bash
VERBOSE=1 ./image/scripts/build-image.sh
```

Notes:

- The manager sets kernel command line `init=/sbin/agent`.
- The rootfs also links `/sbin/init` to `/sbin/agent` as a fallback.
- This gives us direct control of `pid 1` behavior from Go.

### 2. Kernel and boot artifact pipeline (in progress)

Implemented now:

1. Arch-aware kernel artifact build:
   - `image/scripts/build-kernel.sh`
2. Uses Alpine `linux-virt` (good VM default, includes virtio support).
3. Copies kernel artifact to `build/kernel` (symlink `build/vmlinuz`) and stages `/lib/modules` into rootfs.
4. Installs baseline fs/network tooling into rootfs (`nftables`, `iproute2`, `e2fsprogs`, `xfsprogs`, `btrfs-progs`, `ethtool`).
5. Build modes for `build-kernel.sh`:
   - `KERNEL_MODE=auto` (default): package first, source fallback
   - `KERNEL_MODE=package`: package-only
   - `KERNEL_MODE=source`: always compile Linux source (guaranteed ARM64 `Image` on Apple Silicon)

Remaining:

1. Validate and tune kernel command line for lower boot latency.
2. Optional: add custom kernel config/profile dedicated to ultra-fast boot.

### 3. Console and control plane (next)

1. Stabilize serial console interaction from manager to guest.
2. Expose guest connectivity (NAT + reachable service path).
3. Add minimal request protocol beyond `hello world`.

### 4. Container runtime path (later)

1. Add mount/cgroup setup inside guest.
2. Introduce `runc`/containerd or micro-runtime path.
3. Manager API for "run container" requests.

## Manager Usage (current)

From `manager/`, run with local signing + entitlements:

```bash
./run-local.sh \
  --boot-mode linux \
  --kernel ../build/vmlinuz \
  --root-image ../build/rootfs.raw \
  --agent-port 8080 \
  --verbose
```

Why this wrapper: macOS requires the `com.apple.security.virtualization` entitlement to create VMs via `Virtualization.framework`. `run-local.sh` signs `.build/debug/vmmanager` with `manager/vmmanager.entitlements` before execution.

On Apple Silicon, `VZLinuxBootLoader` needs an uncompressed ARM64 Linux `Image` artifact. The manager now performs a preflight check and fails early if you pass an EFI/PE `vmlinuz` by mistake.

EFI mode is now supported:

```bash
./run-local.sh \
  --boot-mode efi \
  --root-image /path/to/efi-bootable-disk.raw
```

Note: current `build/rootfs.raw` is a direct Linux rootfs (ext4, no EFI system partition / bootloader), so it will not boot in EFI mode.

If package kernels resolve to EFI/PE, force source build:

```bash
KERNEL_MODE=source ./image/scripts/build-kernel.sh
```

## Agent Behavior (current)

`agent/cmd/agent/main.go`:

- listens on TCP `:8080` by default
- replies `hello world` to each connection
- includes basic signal handling and zombie reaping logic for `pid 1`

## Practical constraints on macOS

The raw image step needs Linux tooling (`mkfs.ext4`, `e2tools`).

`image/scripts/make-raw-image.sh` tries this order:

1. Host tools (`mkfs.ext4` + `e2tools`)
2. Docker fallback (`ubuntu:24.04`, installs required packages inside the container)

If neither is available, the script exits with setup instructions.

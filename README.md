# vmrunner

Fast-boot Linux microVM experiments on macOS ("firecracker for Macs") using Apple Virtualization.

## Components

- `manager/`: macOS VM manager (Swift + `Virtualization` framework)
- `agent/`: Linux guest agent (Go), currently intended to run as `pid 1`
- `image/`: scripts to build a minimal raw root disk image
- `api/`: Cap'n Proto schemas + generated Go bindings

## Current Status

This repo is now bootstrapped with:

- a manager CLI that builds a `VZVirtualMachineConfiguration`
- a guest agent that connects to host over vsock as soon as guest userspace is up
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
make image
```

Verbose run:

```bash
VERBOSE=1 KERNEL_MODE=source make image
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
3. Add minimal request protocol (implemented: `Agent.ping` over Cap'n Proto RPC).

### 4. Container runtime path (later)

1. Add mount/cgroup setup inside guest.
2. Introduce `runc`/containerd or micro-runtime path.
3. Manager API for "run container" requests.

## Manager Usage (current)

Build VM-related binaries into `build/`:

```bash
make vm-binaries
```

Generate Cap'n Proto Go bindings:

```bash
make api
```

This produces:

- `build/vmmanager` (Swift manager, signed with virtualization entitlement)
- `build/vm` (Go launcher)

Run Swift manager directly:

```bash
make run AGENT_VSOCK_PORT=7000
```

Go wrapper path (SDK-style config + spawn):

```bash
make run-go AGENT_VSOCK_PORT=7000
```

This executes `vm/cmd/vm`, which builds a VM config in Go (`vm/vm.go`) and launches the built Swift manager binary (`build/vmmanager`).

Why this wrapper: macOS requires the `com.apple.security.virtualization` entitlement to create VMs via `Virtualization.framework`. `manager/run-local.sh` now builds/signs `build/vmmanager` with `manager/vmmanager.entitlements` as part of the normal build flow.

On Apple Silicon, `VZLinuxBootLoader` needs an uncompressed ARM64 Linux `Image` artifact. The manager now performs a preflight check and fails early if you pass an EFI/PE `vmlinuz` by mistake.

EFI mode is now supported:

```bash
make run-efi ROOT_IMAGE=/path/to/efi-bootable-disk.raw
```

Note: current `build/rootfs.raw` is a direct Linux rootfs (ext4, no EFI system partition / bootloader), so it will not boot in EFI mode.

If package kernels resolve to EFI/PE, force source build:

```bash
KERNEL_MODE=source make kernel
```

## Agent Behavior (current)

`agent/cmd/agent/main.go`:

- runs as `pid 1` and performs signal handling + zombie reaping
- reads `agent.vsock_port=<port>` from kernel cmdline (default `7000`)
- repeatedly attempts guest->host vsock connect to host CID `2`
- serves Cap'n Proto RPC over that vsock connection (`Agent.ping -> "pong"`)

## Practical constraints on macOS

The raw image step needs Linux tooling (`mkfs.ext4`, `e2tools`).

`image/scripts/make-raw-image.sh` tries this order:

1. Host tools (`mkfs.ext4` + `e2tools`)
2. Docker fallback (`ubuntu:24.04`, installs required packages inside the container)

If neither is available, the script exits with setup instructions.

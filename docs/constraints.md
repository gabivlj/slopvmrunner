# Constraints

## macOS Virtualization

- VM creation on macOS requires `com.apple.security.virtualization` entitlement.
- `manager/run-local.sh` builds and signs `build/vmmanager` with the project entitlements file.

## Kernel Artifact

- Apple Silicon linux boot path expects an uncompressed ARM64 Linux `Image`.
- `build/kernel` should be that artifact (validated by Make targets).

## Networking Model

- Swift manager can attach `VZNATNetworkDeviceAttachment` for guest egress.
- Host-side NAT gateway behavior is managed by macOS vmnet/Virtualization; explicit host bridge/gateway IP control is limited.
- Guest-side interface setup is performed in-agent via kernel APIs (netlink), not `ip` shell commands.

## Build Environment

- Raw image creation requires ext4 tooling; scripts use host tools or container fallback.
- Some CI/sandbox environments may block SwiftPM cache paths or network access for dependency resolution.

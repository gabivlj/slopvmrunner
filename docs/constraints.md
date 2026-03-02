# Constraints

## macOS Virtualization

- VM creation on macOS requires `com.apple.security.virtualization` entitlement.
- `manager/run-local.sh` builds and signs `~/.slopvmrunner/bin/vmmanager` with the project entitlements file.

## Kernel Artifact

- Apple Silicon linux boot path expects an uncompressed ARM64 Linux `Image`.
- `~/.slopvmrunner/kernels/default` should be that artifact (validated by Make targets).

## Networking Model

- Current `run-go` path is vsock-first.
- Networking behavior depends on selected Virtualization attachment mode and entitlements.

## Build Environment

- Raw image creation requires ext4 tooling; scripts use host tools or container fallback.
- Some CI/sandbox environments may block SwiftPM cache paths or network access for dependency resolution.

# Build & Run

## Main Targets

- `make api`: generate Cap'n Proto Go bindings.
- `make agent`: build guest agent binary into `build/agent`.
- `make rootfs`: refresh rootfs tree in `build/rootfs`.
- `make kernel`: build/refresh kernel artifact in `build/kernel`.
- `make image`: build kernel + raw root image (`build/rootfs.raw`).
- `make vm-binaries`: build `build/vmmanager` and `build/vm`.
- `make test`: run VM test suite (requires existing valid kernel artifact).

## Typical Flow

```bash
make image vm-binaries
make run-go
```

`run-go` is the recommended product entrypoint.
`vmmanager` remains available and is used as the backend VM component.
By default, `run-go` is vsock-first.

Run Swift manager directly:

```bash
make run
```

Run EFI path:

```bash
make run-efi
```

## Useful Variables

- `VERBOSE=1`: verbose build script output.
- `KERNEL_MODE=source|package|auto`: kernel build mode.
- `AGENT_VSOCK_PORT=7000`: vsock port used by agent/manager.
- `TEST=Regex`: optional `go test -run` filter used by `make test`.
- `MEMORY_MIB=512`, `CPUS=2`: VM resources.

Example:

```bash
VERBOSE=1 KERNEL_MODE=source make image
```

## Notes

- On Apple Silicon, linux boot mode requires an uncompressed ARM64 Linux `Image`.
- `run-go` uses the Go wrapper in `vm/cmd/vm`, which launches the signed Swift `build/vmmanager` binary.

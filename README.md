# vmrunner

> [!WARNING]
> **Attention:** this is complete slop; just having fun over the weekend with Codex.
> Use at your own risk. This was meant to explore Apple's virtualization networking, see how fast we can start a Linux VM, and see how far I can get with mostly AI-generated codeslop.

Self-contained container runner for macOS, exposed through a Go entrypoint and backed by Linux microVMs.

Cold boot target: ~200ms VM bring-up (measured in this repo’s e2e benchmark path).

## Quickstart

```bash
make image vm-binaries
make run-go
```

Key outputs:

- `build/vm`: Go VM launcher
- `build/kernel`: Linux kernel artifact
- `build/rootfs.raw`: guest root disk

## Repository Layout

- `vm/`: Go runner (primary product entrypoint)
- `manager/`: Swift VM backend component
- `agent/`: Go guest agent (`pid 1` in linux boot mode)
- `api/`: Cap'n Proto schemas and generated Go bindings
- `image/`: rootfs/kernel/image build scripts

## Documentation

- [Build & Run](docs/build-run.md)
- [Architecture](docs/architecture.md)
- [Constraints](docs/constraints.md)

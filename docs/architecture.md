# Architecture

Related docs:

- [Build & Run](build-run.md)
- [Swift Runner Flags](swiftrunner.md)

## Components

- `manager` (Swift): creates and runs `VZVirtualMachine`, configures storage, console, vsock, and optional NAT NIC.
- `vm` (Go): primary entrypoint; builds runtime config, spawns `build/vmmanager`, receives vsock fd from Swift, and bootstraps Cap'n Proto RPC.
- `agent` (Go, guest): runs as `pid 1`, connects to host via vsock, serves Cap'n Proto capabilities.
  - `agent/cmd/agent/debug_service.go`: `Debug` service and byte stream benchmark methods.
  - `agent/cmd/agent/network_service.go`: `Network` service methods.
- `api` (Cap'n Proto): schema and generated Go bindings used by host and guest.

## Control Plane

- Transport: vsock between guest and host.
- RPC: Cap'n Proto over the accepted vsock stream.
- Bootstrap capability: `Agent`.

Current capability split:

- `Agent.network() -> Network`
- `Agent.containerService() -> ContainerService`
- `Network.configureInterface(ifName, cidr, gateway)`
- `Network.setupVsockProxy(port)`
- `ContainerService.create(oci, image, id) -> Container`
- `Container.start(stdout, stderr) -> Task`
- `Task.stdin() -> ByteStream`
- `Task.exitCode() -> Int32`

Additional internal/benchmark capabilities exist on `Agent` but are not part of the stable surface.

## Product Surface

- Supported entrypoint: Go (`build/vm`, `vm` package).
- Swift manager is the backend VM component; the default user-facing entrypoint is Go.

## Boot Sequence (linux mode)

1. Go runner launches Swift manager.
2. Swift manager starts VM and listens for guest vsock connection.
3. Agent in guest connects to host vsock port.
4. Swift manager forwards accepted vsock fd to Go runner over a unix socket.
5. Go runner starts Cap'n Proto RPC and gets `Agent` bootstrap capability.

# Architecture

## Components

- `manager` (Swift): creates and runs `VZVirtualMachine`, configures storage, console, vsock, and optional NAT NIC.
- `vm` (Go): primary entrypoint; builds runtime config, spawns `build/vmmanager`, receives vsock fd from Swift, and bootstraps Cap'n Proto RPC.
- `agent` (Go, guest): runs as `pid 1`, connects to host via vsock, serves Cap'n Proto capabilities.
- `api` (Cap'n Proto): schema and generated Go bindings used by host and guest.

## Control Plane

- Transport: vsock between guest and host.
- RPC: Cap'n Proto over the accepted vsock stream.
- Bootstrap capability: `Agent`.

Current capability split:

- `Agent.debug() -> Debug`
- `Agent.network() -> Network`
- `Debug.ping()`
- `Debug.openByteStream() -> ByteStream`
- `ByteStream.write()/done()`
- `Network.configureInterface(ifName, cidr, gateway)`

## Product Surface

- Supported entrypoint: Go (`build/vm`, `vm` package).
- Swift manager is the backend VM component; the default user-facing entrypoint is Go.

## Boot Sequence (linux mode)

1. Go runner launches Swift manager.
2. Swift manager starts VM and listens for guest vsock connection.
3. Agent in guest connects to host vsock port.
4. Swift manager forwards accepted vsock fd to Go runner over a unix socket.
5. Go runner starts Cap'n Proto RPC and gets `Agent` bootstrap capability.
6. Optional guest network config is applied via `Agent.network()`.

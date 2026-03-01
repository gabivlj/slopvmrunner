# VMRunner Philosophy

This file defines the engineering direction for this repository. Future agents should follow it by default unless the user explicitly overrides it.

## Product Direction

- Goal: build a VM-native container runtime on macOS, in the spirit of Kata, but intentionally minimal and custom.
- Non-goal: adopting a heavy containerd-centered architecture.
- Strategic priority: make vmrunner uniquely strong for transparent proxying and traffic interception of container workloads.
- Preferred stack:
  - Host manager: Swift `Virtualization.framework` runtime + Go orchestration layer.
  - Guest agent: Go.
  - Control plane: Cap'n Proto over vsock.

## Architectural Principles

- Keep control plane simple and measurable:
  - Cap'n Proto RPC on vsock is the primary host<->guest API.
  - Measure latency/throughput continuously and treat regressions seriously.
- Capability-first design:
  - all management/configuration surfaces should be represented as Cap'n Proto capabilities.
  - avoid ad-hoc side channels or non-RPC control paths for core runtime behavior.
- Build incrementally:
  1. solid VM lifecycle and control plane
  2. task/process lifecycle API in guest
  3. namespace/cgroup/mount isolation
  4. OCI bundle compatibility and runtime hardening
- Prefer explicit ownership over daemon sprawl:
  - no default dependency on containerd unless explicitly requested
  - keep runtime logic in this repo and understandable end-to-end

## Runtime Scope (Target)

- Host API eventually should include:
  - create/start/kill/wait/delete task
  - exec in task
  - stats and events
- Guest agent should be the execution authority:
  - receives RPC
  - applies isolation and lifecycle logic
  - reports status/events/metrics back
- Core Cap'n Proto capability surfaces should include:
  - `Network` capability: create/manage network devices, routing, policy, nftables/TPROXY rules.
  - `ContainerManager` capability: create/start/stop/remove/exec/wait container tasks.
  - `ImageManager` capability: pull/list/remove images and expose progress/status.

## Storage Direction

- Base rootfs should remain deterministic and reproducible from image scripts.
- Writable workload state should use attached block devices when appropriate.
- Avoid requiring random host dependencies for critical paths:
  - if ext4 formatting is needed, prefer doing it in Linux-based build paths (existing Docker/image pipeline) rather than assuming host `mkfs`.

## Performance Priorities

- First-class metrics:
  - cold boot to readiness
  - ping RTT
  - control-plane QPS/concurrency
  - streaming throughput for larger payloads
- Keep these benchmarkable in CI/dev (`make test` and dedicated e2e/bench tests).

## Networking and Proxy Model

- By default, workloads should have normal outbound network connectivity.
- vmrunner should make transparent proxy interception easy by design:
  - use TPROXY/nftables-based redirection in guest networking path.
  - route intercepted traffic to a host-side gateway that is scoped per VM (one gateway per VM / Go process).
  - users should be able to plug in their own HTTP CONNECT proxy implementations on top of this gateway model.
- Control and policy configuration for this flow should be delivered via Cap'n Proto capabilities.
- Exception: high-volume network egress dataplane itself may run outside Cap'n Proto RPC when needed for efficiency.

## Coding Expectations

- Changes should preserve:
  - binary outputs in `build/`
  - reproducible build flow via `Makefile`
  - clear failure behavior (do not destroy valid artifacts on failed builds)
- Favor pragmatic, local improvements over framework churn.
- Keep logs structured and useful for debugging benchmark runs.

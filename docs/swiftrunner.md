# Swift Runner Flags

This file documents CLI flags for the Swift VM backend (`~/.slopvmrunner/bin/vmmanager`) and the local wrapper script (`manager/run-local.sh`).

For system design context, see [Architecture](architecture.md).

## `manager/run-local.sh`

Usage:

```bash
./manager/run-local.sh [--out <path>] [--build-only] [-- <vmmanager args>]
```

Flags:

- `--out <path>`: output path for copied/signed `vmmanager` binary.
- `--build-only`: build/sign only, do not execute binary.
- `--`: pass remaining args directly to `vmmanager`.

Env vars:

- `CODESIGN_IDENTITY`: identity name or SHA for codesign.
- `VMMANAGER_ENTITLEMENTS`: entitlements plist path.
  Default: `manager/vmmanager.entitlements` (virtualization only, NAT-friendly).
  Bridged mode requires: `manager/vmmanager.networking.entitlements`.

## `~/.slopvmrunner/bin/vmmanager`

Usage:

```bash
~/.slopvmrunner/bin/vmmanager --root-image <path> [options]
```

Flags:

- `--boot-mode <linux|efi>`: VM boot mode (`linux` default).
- `--kernel <path>`: kernel artifact path (required for `linux` boot mode).
- `--initrd <path>`: optional initrd path.
- `--root-image <path>`: root disk image path (required).
- `--extra-disk <path>`: repeatable extra writable disk image paths.
- `--memory-mib <int>`: VM memory MiB (default `512`).
- `--cpus <int>`: VM CPU count (default `2`).
- `--agent-vsock-port <int>`: vsock port for guest agent connect path (default `7000`).
- `--agent-ready-socket <path>`: unix socket used to deliver accepted vsock fd back to Go process.
- `--enable-network <bool>`: enable NIC attachment (`true` default).
- `--network-mode <nat|bridged|hostonly>`: networking attachment mode (`nat` default for standalone `vmmanager`; Go runner defaults to `hostonly`).
- `--bridge-interface <ifname>`: host interface for bridged/hostonly mode (for example `en0` or `bridge1234`).
- `--vm-network-cidr <cidr>`: optional CIDR passed on kernel cmdline for guest config.
- `--vm-network-gateway <ip>`: optional gateway passed on kernel cmdline for guest config.
- `--vm-network-ifname <name>`: optional ifname passed on kernel cmdline for guest config.
- `--verbose`: enable verbose manager logs.
- `-h`, `--help`: print help.

Notes:

- In bridged mode, if `--bridge-interface` is omitted, the first available `VZBridgedNetworkInterface` is used.
- Hostonly is the default in Go and requires explicit host interface setup.
- Network CIDR/gateway/ifname values are metadata passed through cmdline; guest network setup is performed by the Go flow over Cap'n Proto.

## Troubleshooting

### Error: missing `com.apple.vm.networking` entitlement

If you see an error like:

`Using VZBridgedNetworkDeviceAttachment in a process that lacks the "com.apple.vm.networking" entitlement`

or the process exits with `Killed: 9`, check signing.

1. Use the networking entitlements file for bridged mode:
   - `VMMANAGER_ENTITLEMENTS=manager/vmmanager.networking.entitlements`
   - This file includes both keys:
     - `com.apple.security.virtualization`
     - `com.apple.vm.networking`
2. Ensure you have a valid signing identity:
   - `security find-identity -v -p codesigning`
   - If it shows `0 valid identities found`, create one in Xcode:
     1. Open Xcode.
     2. Go to `Xcode > Settings > Accounts`.
     3. Add/sign in with your Apple ID and select your Team.
     4. Click `Manage Certificates...`.
     5. Add an `Apple Development` certificate.
     6. Re-run `security find-identity -v -p codesigning`.
3. Configure signing identity in `manager/.env.local` (gitignored):

```bash
CODESIGN_IDENTITY_SHA="<SHA1_FROM_SECURITY_FIND_IDENTITY>"
CODESIGN_IDENTITY="Apple Development: Your Name (TEAMID)"
# Needed for bridged mode only:
VMMANAGER_ENTITLEMENTS="manager/vmmanager.networking.entitlements"
```

4. Rebuild/sign:

```bash
make vm-binaries
```

5. Verify entitlements on binary:

```bash
codesign -d --entitlements :- ~/.slopvmrunner/bin/vmmanager
```

Template file:
- `manager/signing.env.example`

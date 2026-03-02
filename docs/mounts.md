# Mount Setup

This is the current container mount model used by `run-container` (virtiofs mode).

## Host Side

- Shared virtiofs directory (default): `build/virtiofs`
- Image rootfs materialization path:
  - `build/virtiofs/<image-manifest-sha256-hex>/rootfs`
- Writable state disk file:
  - `build/container-state.raw` (ext4)

## Guest Side

- Virtiofs mountpoint (default): `/var/run/vmrunner`
- Writable state disk mountpoint (default): `/mnt/containers`

Given:
- `containerID = <id>`
- `imageHash = <manifest-sha256-hex>`

The paths are:

- Lower rootfs (read-only image contents):
  - `/var/run/vmrunner/<imageHash>/rootfs`
- Bundle dir:
  - `/mnt/containers/<id>/bundle`
- OCI config:
  - `/mnt/containers/<id>/bundle/config.json`
- Overlay writable paths:
  - `/mnt/containers/<id>/overlays/diff`
  - `/mnt/containers/<id>/overlays/work`
- Overlay merged rootfs (for `runc` root.path=`rootfs`):
  - `/mnt/containers/<id>/bundle/rootfs`

## Runtime Flow

1. Runner resolves/pulls image and prepares shared rootfs under `build/virtiofs/<imageHash>/rootfs`.
2. Runner creates/attaches writable ext4 state disk and passes mount hints via kernel cmdline.
3. Agent mounts virtiofs at `/var/run/vmrunner` and state disk at `/mnt/containers`.
4. Runner calls `ContainerService.create(oci, image, id, rootfsPath, containerStateDisk)`.
5. Agent creates bundle at `/mnt/containers/<id>/bundle`, writes `config.json`, mounts overlay using:
   - lower=`rootfsPath`
   - upper/work in `/mnt/containers/<id>/overlays`
   - merged=`/mnt/containers/<id>/bundle/rootfs`
6. Agent executes `runc run --bundle /mnt/containers/<id>/bundle <id>`.

## Notes

- `rootfsPath` and `containerStateDisk` are decided by the runner and passed over Cap'n Proto; agent should not recalculate them.
- The default generated OCI spec uses `root.path = "rootfs"` so it resolves inside the bundle dir.

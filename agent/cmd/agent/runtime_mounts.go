//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

func setupRuntimeMounts() error {
	type mountSpec struct {
		target   string
		fstype   string
		source   string
		flags    uintptr
		data     string
		optional bool
	}
	specs := []mountSpec{
		{target: "/proc", fstype: "proc", source: "proc"},
		{target: "/sys", fstype: "sysfs", source: "sysfs"},
		{target: "/dev", fstype: "devtmpfs", source: "devtmpfs", optional: true},
		{target: "/sys/fs/cgroup", fstype: "cgroup2", source: "cgroup2", optional: true},
	}
	for _, s := range specs {
		if err := os.MkdirAll(s.target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", s.target, err)
		}
		if err := syscall.Mount(s.source, s.target, s.fstype, s.flags, s.data); err != nil {
			// Already mounted is fine.
			if err == syscall.EBUSY {
				continue
			}
			// Optional mounts may be unavailable depending on kernel config.
			if s.optional {
				continue
			}
			return fmt.Errorf("mount %s on %s: %w", s.fstype, s.target, err)
		}
	}

	// Fallback: if unified cgroup2 mount isn't available, try legacy cgroup v1.
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		_ = syscall.Mount("cgroup", "/sys/fs/cgroup", "cgroup", 0, "")
	}

	if virtioFSEnabled() {
		tag := virtioFSTag()
		mountPoint := virtioFSMountPoint()
		if err := os.MkdirAll(mountPoint, 0o755); err != nil {
			return fmt.Errorf("mkdir virtiofs mountpoint %s: %w", mountPoint, err)
		}
		if err := syscall.Mount(tag, mountPoint, "virtiofs", 0, ""); err != nil && err != syscall.EBUSY {
			if err == syscall.ENODEV {
				return fmt.Errorf("mount virtiofs tag=%s mountPoint=%s: %w (guest kernel likely missing CONFIG_VIRTIO_FS)", tag, mountPoint, err)
			}
			return fmt.Errorf("mount virtiofs tag=%s mountPoint=%s: %w", tag, mountPoint, err)
		}
	}
	return nil
}

func virtioFSEnabled() bool {
	for _, arg := range readKernelCmdline() {
		if arg == "agent.virtiofs=1" || arg == "agent.virtiofs=true" {
			return true
		}
	}
	return false
}

func virtioFSTag() string {
	for _, arg := range readKernelCmdline() {
		if strings.HasPrefix(arg, "agent.virtiofs_tag=") {
			v := strings.TrimSpace(strings.TrimPrefix(arg, "agent.virtiofs_tag="))
			if v != "" {
				return v
			}
		}
	}
	return "vmrunnerfs0"
}

func virtioFSMountPoint() string {
	for _, arg := range readKernelCmdline() {
		if strings.HasPrefix(arg, "agent.virtiofs_mount=") {
			v := strings.TrimSpace(strings.TrimPrefix(arg, "agent.virtiofs_mount="))
			if v != "" {
				return v
			}
		}
	}
	return "/run/vmrunner/shared"
}

func overlayStateDevice() string {
	for _, arg := range readKernelCmdline() {
		if strings.HasPrefix(arg, "agent.overlay_device=") {
			v := strings.TrimSpace(strings.TrimPrefix(arg, "agent.overlay_device="))
			if v != "" {
				return v
			}
		}
	}
	return "/dev/vdb"
}

func overlayStateMountPoint() string {
	for _, arg := range readKernelCmdline() {
		if strings.HasPrefix(arg, "agent.overlay_mount=") {
			v := strings.TrimSpace(strings.TrimPrefix(arg, "agent.overlay_mount="))
			if v != "" {
				return v
			}
		}
	}
	return "/mnt/containers"
}

//go:build linux

package main

import (
	"fmt"
	"os"
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
	return nil
}

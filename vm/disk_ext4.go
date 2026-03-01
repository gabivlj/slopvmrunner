package vm

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func CreateExt4Disk(ctx context.Context, diskPath string, sizeMiB int, label string) error {
	return CreateExt4DiskFromDir(ctx, diskPath, sizeMiB, label, "")
}

func CreateExt4DiskFromDir(ctx context.Context, diskPath string, sizeMiB int, label, srcDir string) error {
	if sizeMiB <= 0 {
		return fmt.Errorf("sizeMiB must be > 0")
	}
	label = cmp.Or(label, "vmrunner-data")
	if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
		return fmt.Errorf("create disk directory: %w", err)
	}

	f, err := os.Create(diskPath)
	if err != nil {
		return fmt.Errorf("create disk file %q: %w", diskPath, err)
	}
	sizeBytes := int64(sizeMiB) * 1024 * 1024
	if err := f.Truncate(sizeBytes); err != nil {
		_ = f.Close()
		return fmt.Errorf("truncate disk file %q: %w", diskPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close disk file %q: %w", diskPath, err)
	}

	mkfsPath, err := exec.LookPath("mkfs.ext4")
	if err != nil {
		return fmt.Errorf("mkfs.ext4 not found in PATH")
	}
	args := []string{"-F", "-L", label}
	if srcDir != "" {
		args = append(args, "-d", srcDir)
	}
	args = append(args, diskPath)
	cmd := exec.CommandContext(ctx, mkfsPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 failed: %w (output: %s)", err, string(out))
	}
	return nil
}

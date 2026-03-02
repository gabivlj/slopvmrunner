package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultHomeStateDirName = ".slopvmrunner"
	DefaultWorkStateDirName = ".slopvmrunner"
)

type VMRunnerState struct {
	HomeRoot string
	WorkRoot string
	VMName   string

	ImagesDir        string
	VMsDir           string
	VMDir            string
	LockFilePath     string
	ReadySocketPath  string
	VirtioFSHostDir  string
	ContainerState   string
	DefaultOCISpec   string
	VMRootImagePath  string
	KernelPath       string
	BaseRootImage    string
	ManagerBinary    string
	RunnerBinaryPath string
}

func DefaultVMName() string {
	return fmt.Sprintf("vm-%d", time.Now().Unix())
}

func ResolveVMRunnerState(cwd, vmName, homeRoot, workRoot string) (VMRunnerState, error) {
	if cwd == "" {
		return VMRunnerState{}, fmt.Errorf("cwd is required")
	}

	if vmName == "" {
		vmName = DefaultVMName()
	}

	if homeRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return VMRunnerState{}, fmt.Errorf("resolve user home: %w", err)
		}
		homeRoot = filepath.Join(home, DefaultHomeStateDirName)
	}

	if workRoot == "" {
		workRoot = filepath.Join(cwd, DefaultWorkStateDirName)
	}

	vmDir := filepath.Join(workRoot, "vms", vmName)
	state := VMRunnerState{
		HomeRoot:         homeRoot,
		WorkRoot:         workRoot,
		VMName:           vmName,
		ImagesDir:        filepath.Join(workRoot, "images"),
		VMsDir:           filepath.Join(workRoot, "vms"),
		VMDir:            vmDir,
		LockFilePath:     filepath.Join(vmDir, "vm.lock"),
		ReadySocketPath:  filepath.Join(vmDir, "agent-ready.sock"),
		VirtioFSHostDir:  filepath.Join(workRoot, "images"),
		ContainerState:   filepath.Join(vmDir, "container-state.raw"),
		DefaultOCISpec:   filepath.Join(vmDir, "oci-default.json"),
		VMRootImagePath:  filepath.Join(vmDir, "rootfs.raw"),
		KernelPath:       filepath.Join(homeRoot, "kernels", "default"),
		BaseRootImage:    filepath.Join(homeRoot, "rootfs", "default.raw"),
		ManagerBinary:    filepath.Join(homeRoot, "bin", "vmmanager"),
		RunnerBinaryPath: filepath.Join(homeRoot, "bin", "vm"),
	}

	return state, nil
}

func (s VMRunnerState) EnsureDirs() error {
	dirs := []string{
		s.HomeRoot,
		filepath.Join(s.HomeRoot, "bin"),
		filepath.Join(s.HomeRoot, "kernels"),
		filepath.Join(s.HomeRoot, "rootfs"),
		s.WorkRoot,
		s.ImagesDir,
		s.VMsDir,
		s.VMDir,
		s.VirtioFSHostDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create state dir %q: %w", d, err)
		}
	}
	return nil
}

func (s VMRunnerState) EnsureVMRootImage() error {
	if _, err := os.Stat(s.VMRootImagePath); err == nil {
		return nil
	}
	src, err := os.Open(s.BaseRootImage)
	if err != nil {
		return fmt.Errorf("open base root image %q: %w", s.BaseRootImage, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(s.VMRootImagePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create vm root image %q: %w", s.VMRootImagePath, err)
	}
	defer dst.Close()

	if _, err := dst.ReadFrom(src); err != nil {
		return fmt.Errorf("copy base root image to vm root image: %w", err)
	}
	return nil
}

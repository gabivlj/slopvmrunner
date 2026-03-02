package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"vmrunner/vm"
)

func main() {
	cfg := vm.VMConfig{}
	bootModeRaw := string(vm.BootModeLinux)
	networkModeRaw := string(vm.NetworkModeHostOnly)
	vmName := vm.DefaultVMName()
	workStateRoot := ""
	homeStateRoot := ""
	vmManagerPath := ""
	containerImageRef := ""
	containerSharedHostDir := ""
	virtioFSTag := "vmrunnerfs0"
	virtioFSMountPoint := "/var/run/vmrunner"
	containerStateMount := "/mnt/containers"
	containerDiskSizeMiB := 4096
	containerDiskLabel := "vmrunner-data"
	containerRootfsPath := ""
	containerStateDiskGuestPath := "/var/run/vmrunner"
	ociSpecPath := ""
	generatedSpecOut := ""
	containerID := "vmrunner-container"

	flag.StringVar(&vmName, "vm-name", vmName, "VM name (used under work state dir)")
	flag.StringVar(&workStateRoot, "work-state-root", "", "Work state root (default: <cwd>/.slopvmrunner)")
	flag.StringVar(&homeStateRoot, "home-state-root", "", "Home state root (default: ~/.slopvmrunner)")
	flag.StringVar(&vmManagerPath, "vmmanager", "", "Path to vmmanager binary (default: <home-state-root>/bin/vmmanager)")
	flag.StringVar(&bootModeRaw, "boot-mode", string(vm.BootModeLinux), "Boot mode: linux or efi")
	flag.StringVar(&cfg.KernelPath, "kernel", "", "Kernel artifact path (default: <home-state-root>/kernels/default)")
	flag.StringVar(&cfg.InitrdPath, "initrd", "", "Initrd path")
	flag.StringVar(&cfg.RootImage, "root-image", "", "Root disk image path (default: vm-specific path in work state)")
	flag.IntVar(&cfg.MemoryMiB, "memory-mib", 512, "VM memory in MiB")
	flag.IntVar(&cfg.CPUs, "cpus", 2, "VM CPU count")
	flag.IntVar(&cfg.AgentVsockPort, "agent-vsock-port", 7000, "Agent vsock port to pass via kernel cmdline")
	flag.StringVar(&cfg.AgentReadySocketPath, "agent-ready-socket", "", "Unix socket path for readiness notification")
	flag.BoolVar(&cfg.EnableNetwork, "enable-network", false, "Attach a VM network device")
	flag.StringVar(&networkModeRaw, "network-mode", string(vm.NetworkModeHostOnly), "VM network mode: nat, bridged, or hostonly")
	flag.StringVar(&cfg.BridgeInterface, "bridge-interface", "", "Host interface for bridged/hostonly mode (empty = auto)")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose logs and guest console output")
	flag.StringVar(&containerImageRef, "container-image", "", "OCI image reference used by RunContainer (required when running a container)")
	flag.StringVar(&containerSharedHostDir, "container-shared-host-dir", "", "Host directory used for virtio-fs container rootfs sharing")
	flag.StringVar(&virtioFSTag, "virtiofs-tag", "vmrunnerfs0", "Virtio-fs tag")
	flag.StringVar(&virtioFSMountPoint, "virtiofs-mount-point", "/var/run/vmrunner", "Virtio-fs guest mount point hint")
	flag.StringVar(&containerStateMount, "container-state-mount", "/mnt/containers", "Guest mountpoint for writable overlay state disk")
	flag.IntVar(&containerDiskSizeMiB, "container-disk-size-mib", 4096, "Size of extra ext4 disk")
	flag.StringVar(&containerDiskLabel, "container-disk-label", "vmrunner-data", "Filesystem label for writable container state disk")
	flag.StringVar(&ociSpecPath, "oci-spec", "", "Optional path to OCI config.json to run inside guest")
	flag.StringVar(&generatedSpecOut, "oci-default-out", "", "Where to write generated default OCI spec")
	flag.StringVar(&containerID, "container-id", "vmrunner-container", "Container ID for OCI run")
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	state, err := vm.ResolveVMRunnerState(cwd, vmName, homeStateRoot, workStateRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := state.EnsureDirs(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if cfg.KernelPath == "" {
		cfg.KernelPath = state.KernelPath
	}
	if cfg.RootImage == "" {
		if err := state.EnsureVMRootImage(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		cfg.RootImage = state.VMRootImagePath
	}
	if cfg.AgentReadySocketPath == "" {
		cfg.AgentReadySocketPath = state.ReadySocketPath
	}
	if containerSharedHostDir == "" {
		containerSharedHostDir = state.VirtioFSHostDir
	}
	if generatedSpecOut == "" {
		generatedSpecOut = state.DefaultOCISpec
	}
	if vmManagerPath == "" {
		vmManagerPath = state.ManagerBinary
	}

	bootMode, err := vm.ParseBootMode(bootModeRaw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg.BootMode = bootMode
	networkMode, err := vm.ParseNetworkMode(networkModeRaw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg.NetworkMode = networkMode

	logLevel := slog.LevelError
	if cfg.Verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if ociSpecPath != "" && containerImageRef == "" {
		logger.Error("container-image is required when oci-spec is set")
		os.Exit(2)
	}

	if containerImageRef != "" || ociSpecPath != "" {
		if containerImageRef == "" {
			logger.Error("container-image is required for virtiofs mode")
			os.Exit(2)
		}

		if err := os.MkdirAll(containerSharedHostDir, 0o755); err != nil {
			logger.Error("create virtiofs host dir failed", "error", err)
			os.Exit(1)
		}

		cfg.EnableVirtioFS = true
		absSharedDir, err := filepath.Abs(containerSharedHostDir)
		if err != nil {
			logger.Error("resolve virtiofs host dir failed", "error", err)
			os.Exit(1)
		}

		cfg.VirtioFSHostDir = absSharedDir
		cfg.VirtioFSTag = virtioFSTag
		cfg.VirtioFSMountPoint = virtioFSMountPoint
		cfg.OverlayStateDevice = "/dev/vdb"
		cfg.OverlayStateMount = containerStateMount
		containerStateDiskGuestPath = cfg.OverlayStateMount
		stateDiskPath := state.ContainerState
		if err := vm.CreateExt4Disk(ctx, stateDiskPath, containerDiskSizeMiB, "vmrunner-state"); err != nil {
			logger.Error("create container state disk failed", "error", err)
			os.Exit(1)
		}

		absStateDisk, err := filepath.Abs(stateDiskPath)
		if err != nil {
			logger.Error("resolve container state disk path failed", "error", err)
			os.Exit(1)
		}

		cfg.ExtraDiskPaths = append(cfg.ExtraDiskPaths, absStateDisk)
		imageHash, _, err := vm.PrepareSharedContainerRootFS(ctx, containerImageRef, cfg.VirtioFSHostDir)
		if err != nil {
			logger.Error("prepare shared container rootfs failed", "error", err)
			os.Exit(1)
		}

		containerRootfsPath = filepath.Join(cfg.VirtioFSMountPoint, imageHash, "rootfs")
		hostRootfsPath := filepath.Join(cfg.VirtioFSHostDir, imageHash, "rootfs")
		if _, err := os.Stat(hostRootfsPath); err != nil {
			logger.Error("prepared virtiofs rootfs missing on host", "path", hostRootfsPath, "error", err)
			os.Exit(1)
		}

		if cfg.Verbose {
			logger.Debug("virtiofs configured", "host_dir", absSharedDir, "tag", virtioFSTag, "mount_point", virtioFSMountPoint)
		}
	}

	now := time.Now()
	runner := vm.NewVMRunnerWithManager(logger, vmManagerPath)
	vmCtx, err := runner.Run(ctx, cfg)
	if err != nil {
		logger.Error("vm runner failed", "error", err)
		os.Exit(1)
	}
	defer vmCtx.Close()

	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	pingNow := time.Now()
	agent := vmCtx.Agent()
	defer agent.Release()
	debugFuture, debugRelease := agent.Debug(pingCtx, nil)
	defer debugRelease()
	debugRes, err := debugFuture.Struct()
	if err != nil {
		logger.Error("agent debug capability failed", "error", err)
		_ = vmCtx.Kill()
		os.Exit(1)
	}
	debug := debugRes.Debug()
	defer debug.Release()

	pingFuture, release := debug.Ping(pingCtx, nil)
	defer release()
	pingRes, err := pingFuture.Struct()
	if err != nil {
		logger.Error("agent ping failed", "error", err)
		_ = vmCtx.Kill()
		os.Exit(1)
	}
	msg, err := pingRes.Message_()
	if err != nil {
		logger.Error("agent ping decode failed", "error", err)
		_ = vmCtx.Kill()
		os.Exit(1)
	}

	logger.Info("agent ping ok", "message", msg, "time to e2e", time.Since(now), "pong latency", time.Since(pingNow))

	var specJSON []byte
	if ociSpecPath != "" {
		rawSpec, err := os.ReadFile(ociSpecPath)
		if err != nil {
			logger.Error("read oci spec failed", "path", ociSpecPath, "error", err)
			_ = vmCtx.Kill()
			os.Exit(1)
		}
		specJSON = rawSpec
	} else if containerImageRef != "" {
		rawSpec, err := vm.BuildDefaultOCISpecJSON(vm.DefaultOCISpecOptions{
			ImageRef:      containerImageRef,
			RootfsPath:    "rootfs",
			ContainerName: containerID,
		})
		if err != nil {
			logger.Error("generate default oci spec failed", "error", err)
			_ = vmCtx.Kill()
			os.Exit(1)
		}
		specJSON = rawSpec
		if err := os.MkdirAll(filepath.Dir(generatedSpecOut), 0o755); err == nil {
			if err := os.WriteFile(generatedSpecOut, rawSpec, 0o644); err == nil {
				logger.Info("generated default oci spec", "path", generatedSpecOut)
			}
		}
	}

	if len(specJSON) > 0 {
		runCtx, runCancel := context.WithTimeout(ctx, 5*time.Minute)
		defer runCancel()
		runRes, err := vm.RunContainer(runCtx, agent, containerID, containerImageRef, containerRootfsPath, containerStateDiskGuestPath, specJSON)
		if err != nil {
			logger.Error("run oci failed", "error", err)
			_ = vmCtx.Kill()
			os.Exit(1)
		}
		logger.Info("run container finished", "container_id", containerID, "exit_code", runRes.ExitCode)
	}

	if err := <-vmCtx.WaitCh(); err != nil {
		logger.Error("vm exited with error", "error", err)
		os.Exit(1)
	}
}

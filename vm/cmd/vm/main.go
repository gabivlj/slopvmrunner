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
	containerImageRef := ""
	containerDiskPath := ""
	containerDiskSizeMiB := 4096
	containerDiskLabel := "vmrunner-data"
	ociSpecPath := ""
	generatedSpecOut := "build/oci-default.json"
	containerID := "vmrunner-container"

	flag.StringVar(&bootModeRaw, "boot-mode", string(vm.BootModeLinux), "Boot mode: linux or efi")
	flag.StringVar(&cfg.KernelPath, "kernel", "build/kernel", "Kernel artifact path (required in linux mode)")
	flag.StringVar(&cfg.InitrdPath, "initrd", "", "Initrd path")
	flag.StringVar(&cfg.RootImage, "root-image", "build/rootfs.raw", "Root disk image path")
	flag.IntVar(&cfg.MemoryMiB, "memory-mib", 512, "VM memory in MiB")
	flag.IntVar(&cfg.CPUs, "cpus", 2, "VM CPU count")
	flag.IntVar(&cfg.AgentVsockPort, "agent-vsock-port", 7000, "Agent vsock port to pass via kernel cmdline")
	flag.StringVar(&cfg.AgentReadySocketPath, "agent-ready-socket", "build/agent-ready.sock", "Unix socket path for readiness notification")
	flag.BoolVar(&cfg.EnableNetwork, "enable-network", false, "Attach a VM network device")
	flag.StringVar(&networkModeRaw, "network-mode", string(vm.NetworkModeHostOnly), "VM network mode: nat, bridged, or hostonly")
	flag.StringVar(&cfg.BridgeInterface, "bridge-interface", "", "Host interface for bridged/hostonly mode (empty = auto)")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose logs and guest console output")
	flag.StringVar(&containerImageRef, "container-image", "", "OCI image reference used by RunContainer (required when running a container)")
	flag.StringVar(&containerDiskPath, "container-disk", "", "Optional explicit path to extra ext4 disk attached to VM (empty = cached by manifest digest)")
	flag.IntVar(&containerDiskSizeMiB, "container-disk-size-mib", 4096, "Size of extra ext4 disk")
	flag.StringVar(&containerDiskLabel, "container-disk-label", "vmrunner-data", "Filesystem label for extra ext4 disk")
	flag.StringVar(&ociSpecPath, "oci-spec", "", "Optional path to OCI config.json to run inside guest")
	flag.StringVar(&generatedSpecOut, "oci-default-out", "build/oci-default.json", "Where to write generated default OCI spec")
	flag.StringVar(&containerID, "container-id", "vmrunner-container", "Container ID for OCI run")
	flag.Parse()

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
		if containerImageRef != "" {
			if containerDiskPath == "" {
				cachePath, built, err := vm.PrepareImageExt4Disk(ctx, containerImageRef, "build/image-disks", containerDiskSizeMiB, containerDiskLabel)
				if err != nil {
					logger.Error("prepare cached image disk failed", "error", err)
					os.Exit(1)
				}
				containerDiskPath = cachePath
				if cfg.Verbose {
					logger.Debug("image disk prepared", "path", containerDiskPath, "built", built)
				}
			} else {
				logger.Info("building ext4 image disk from registry image", "image", containerImageRef, "path", containerDiskPath, "size_mib", containerDiskSizeMiB)
				if err := vm.BuildImageExt4Disk(ctx, containerImageRef, containerDiskPath, containerDiskSizeMiB, containerDiskLabel); err != nil {
					logger.Error("build image ext4 disk failed", "error", err)
					os.Exit(1)
				}
			}
		} else {
			logger.Info("creating ext4 container disk", "path", containerDiskPath, "size_mib", containerDiskSizeMiB)
			if err := vm.CreateExt4Disk(ctx, containerDiskPath, containerDiskSizeMiB, containerDiskLabel); err != nil {
				logger.Error("create ext4 disk failed", "error", err)
				os.Exit(1)
			}
		}
		absDiskPath, err := filepath.Abs(containerDiskPath)
		if err != nil {
			logger.Error("resolve container disk path failed", "error", err)
			os.Exit(1)
		}
		cfg.ExtraDiskPaths = append(cfg.ExtraDiskPaths, absDiskPath)
	}

	now := time.Now()
	runner := vm.NewVMRunner(logger)
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
			RootfsPath:    fmt.Sprintf("/run/vmrunner/containers/%s/rootfs", containerID),
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
		runRes, err := vm.RunContainer(runCtx, agent, containerID, containerImageRef, specJSON)
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

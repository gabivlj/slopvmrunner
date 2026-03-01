package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vmrunner/vm"
)

func main() {
	cfg := vm.VMConfig{}
	bootModeRaw := string(vm.BootModeLinux)
	networkModeRaw := string(vm.NetworkModeHostOnly)

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
	flag.BoolVar(&cfg.Verbose, "verbose", true, "Enable vmmanager verbose logs")
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

	logLevel := slog.LevelInfo
	if cfg.Verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	if err := <-vmCtx.WaitCh(); err != nil {
		logger.Error("vm exited with error", "error", err)
		os.Exit(1)
	}
}

package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBenchmarkAccepted(t *testing.T) {
	repoRoot := filepath.Clean("..")
	kernelPath := filepath.Join(repoRoot, "build", "kernel")
	rootImagePath := filepath.Join(repoRoot, "build", "rootfs.raw")
	managerPath := filepath.Join(repoRoot, "build", "vmmanager")

	for _, p := range []string{kernelPath, rootImagePath, managerPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing required artifact %q: %v (run `make image vm-binaries`)", p, err)
		}
	}

	readySock := filepath.Join(t.TempDir(), "agent-ready.sock")

	cfg := VMConfig{
		BootMode:             BootModeLinux,
		KernelPath:           kernelPath,
		RootImage:            rootImagePath,
		MemoryMiB:            512,
		CPUs:                 2,
		AgentVsockPort:       7000,
		AgentReadySocketPath: readySock,
		Verbose:              true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	runner := NewVMRunner(nil)
	runner.ManagerBinaryPath = managerPath

	bootStart := time.Now()
	vmCtx, err := runner.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("run vm: %v", err)
	}
	defer vmCtx.Close()
	e2e := time.Since(bootStart)

	pingStart := time.Now()
	agent := vmCtx.Agent()
	defer agent.Release()
	fut, release := agent.Ping(ctx, nil)
	defer release()

	res, err := fut.Struct()
	if err != nil {
		t.Fatalf("agent ping call failed: %v", err)
	}
	msg, err := res.Message_()
	if err != nil {
		t.Fatalf("decode ping result: %v", err)
	}
	pongLatency := time.Since(pingStart)

	if msg != "pong" {
		t.Fatalf("unexpected ping response: %q", msg)
	}

	t.Logf("agent ping ok message=%s time to e2e=%s pong latency=%s", msg, e2e, pongLatency)

	if err := vmCtx.Kill(); err != nil {
		t.Fatalf("kill vm: %v", err)
	}

	select {
	case err := <-vmCtx.WaitCh():
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("vm exited with error after kill (accepted): %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for vm process to exit after kill")
	}
}

package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
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
		MemoryMiB:            4096,
		CPUs:                 8,
		AgentVsockPort:       7000,
		AgentReadySocketPath: readySock,
		Verbose:              true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
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
	debugFut, debugRelease := agent.Debug(ctx, nil)
	defer debugRelease()
	debugRes, err := debugFut.Struct()
	if err != nil {
		t.Fatalf("agent debug call failed: %v", err)
	}
	debug := debugRes.Debug()
	defer debug.Release()

	fut, release := debug.Ping(ctx, nil)
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
	if e2e >= time.Second {
		t.Fatalf("time to e2e too high: %s (want < 1s)", e2e)
	}

	const streamCount = 15
	const totalBytesPerStream = 1024 * 1024 * 1024 // 1 GiB

	// Testing capnp throughput

	streams := make([]vmapi.ByteStream, 0, streamCount)
	for i := 0; i < streamCount; i++ {
		openFut, openRelease := debug.OpenByteStream(ctx, nil)
		openRes, err := openFut.Struct()
		if err != nil {
			t.Fatalf("open byte stream[%d]: %v", i, err)
		}

		streams = append(streams, openRes.Stream().AddRef())
		openRelease()
	}

	defer func() {
		for _, stream := range streams {
			stream.Release()
		}
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, streamCount)
	perStreamDur := make([]time.Duration, streamCount)
	perStreamBytes := make([]int, streamCount)
	aggregateStart := time.Now()

	for i := 0; i < streamCount; i++ {
		idx := i
		stream := streams[i]
		wg.Go(func() {
			chunk := make([]byte, 16*1024*1024)
			wrote := 0
			writeStart := time.Now()
			for wrote < totalBytesPerStream {
				remaining := totalBytesPerStream - wrote
				toSend := len(chunk)
				if remaining < toSend {
					toSend = remaining
				}
				if err := stream.Write(ctx, func(p vmapi.ByteStream_write_Params) error {
					return p.SetChunk(chunk[:toSend])
				}); err != nil {
					errCh <- err
					return
				}
				wrote += toSend
			}

			doneFut, doneRelease := stream.Done(ctx, nil)
			_, err := doneFut.Struct()
			doneRelease()
			if err != nil {
				errCh <- err
				return
			}

			perStreamDur[idx] = time.Since(writeStart)
			perStreamBytes[idx] = wrote
		})
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("byte stream transfer failed: %v", err)
		}
	}

	aggregateDur := time.Since(aggregateStart)
	aggregateBytes := 0
	for i := 0; i < streamCount; i++ {
		aggregateBytes += perStreamBytes[i]
		throughputBps := float64(perStreamBytes[i]) / perStreamDur[i].Seconds()
		t.Logf("byte stream[%d] throughput bytes_per_sec=%.2f bytes=%d duration=%s", i, throughputBps, perStreamBytes[i], perStreamDur[i])
	}
	aggregateThroughputBps := float64(aggregateBytes) / aggregateDur.Seconds()
	t.Logf("byte stream aggregate throughput bytes_per_sec=%.2f bytes=%d duration=%s streams=%d", aggregateThroughputBps, aggregateBytes, aggregateDur, streamCount)

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

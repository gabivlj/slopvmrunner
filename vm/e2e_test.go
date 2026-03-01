package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
)

type benchStreamSink struct {
	mu      sync.Mutex
	firstAt time.Time
	bytes   int
	buf     []byte
}

func (s *benchStreamSink) Write(_ context.Context, call vmapi.ByteStream_write) error {
	chunk, err := call.Args().Chunk()
	if err != nil {
		return err
	}
	if len(chunk) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.firstAt.IsZero() {
		s.firstAt = time.Now()
	}
	s.bytes += len(chunk)
	s.buf = append(s.buf, chunk...)
	s.mu.Unlock()
	return nil
}

func (s *benchStreamSink) Done(_ context.Context, _ vmapi.ByteStream_done) error {
	return nil
}

func (s *benchStreamSink) snapshot() (time.Time, int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstAt, s.bytes, string(s.buf)
}

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

func TestBenchmarkVsock(t *testing.T) {
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

	const (
		benchConnCount = 15
		benchPort      = 7100
		benchTotalByte = uint64(1024 * 1024 * 1024) // 1 GiB per connection
		benchChunkByte = uint32(1024 * 1024)        // 1 MiB
	)

	cfg := VMConfig{
		BootMode:             BootModeLinux,
		KernelPath:           kernelPath,
		RootImage:            rootImagePath,
		MemoryMiB:            4096,
		CPUs:                 8,
		AgentVsockPort:       7000,
		ProxyVsockPorts:      []int{benchPort},
		AgentReadySocketPath: readySock,
		Verbose:              true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	runner := NewVMRunner(nil)
	runner.ManagerBinaryPath = managerPath

	vmCtx, err := runner.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("run vm: %v", err)
	}
	defer vmCtx.Close()

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

	networkFut, networkRelease := agent.Network(ctx, nil)
	defer networkRelease()
	networkRes, err := networkFut.Struct()
	if err != nil {
		t.Fatalf("agent network call failed: %v", err)
	}
	network := networkRes.Network()
	defer network.Release()

	setupFut, setupRelease := network.SetupVsockProxy(ctx, func(params vmapi.Network_setupVsockProxy_Params) error {
		params.SetPort(benchPort)
		return nil
	})
	if _, err := setupFut.Struct(); err != nil {
		setupRelease()
		t.Fatalf("setup vsock proxy failed port=%d: %v", benchPort, err)
	}
	setupRelease()

	type hostConnMetric struct {
		idx      int
		bytes    int64
		ttfb     time.Duration
		duration time.Duration
		err      error
	}
	type guestConnMetric struct {
		idx           int
		bytesPerSec   float64
		durationNanos uint64
		err           error
	}

	startTimes := make([]time.Time, benchConnCount)
	var startMu sync.Mutex
	hostAcceptCount := 0

	hostMetricsCh := make(chan hostConnMetric, benchConnCount)
	go func() {
		for accepted := range vmCtx.ProxyConnCh() {
			if accepted.File == nil {
				continue
			}
			if accepted.Port != benchPort {
				if accepted.File != nil {
					_ = accepted.File.Close()
				}
				continue
			}
			go func(accepted ProxyConn) {
				startMu.Lock()
				idx := hostAcceptCount
				hostAcceptCount++
				var start time.Time
				if idx < len(startTimes) {
					start = startTimes[idx]
				}
				startMu.Unlock()
				if start.IsZero() {
					_ = accepted.File.Close()
					hostMetricsCh <- hostConnMetric{
						idx: idx,
						err: errors.New("missing start time for accepted benchmark connection"),
					}
					return
				}

				firstBuf := make([]byte, 64*1024)
				firstReadStart := time.Now()
				nFirst, readErr := accepted.File.Read(firstBuf)
				if nFirst <= 0 && readErr != nil {
					_ = accepted.File.Close()
					hostMetricsCh <- hostConnMetric{
						idx:  idx,
						err:  readErr,
						ttfb: time.Since(start),
					}
					return
				}
				firstByteAt := time.Now()
				ttfb := firstByteAt.Sub(start)

				copiedTail, copyErr := io.Copy(io.Discard, accepted.File)
				_ = accepted.File.Close()
				totalBytes := int64(nFirst) + copiedTail
				if readErr != nil && !errors.Is(readErr, io.EOF) {
					hostMetricsCh <- hostConnMetric{
						idx:  idx,
						ttfb: ttfb,
						err:  readErr,
					}
					return
				}
				if copyErr != nil {
					hostMetricsCh <- hostConnMetric{
						idx:  idx,
						ttfb: ttfb,
						err:  copyErr,
					}
					return
				}
				hostMetricsCh <- hostConnMetric{
					idx:      idx,
					bytes:    totalBytes,
					ttfb:     ttfb,
					duration: time.Since(firstReadStart),
				}
			}(accepted)
		}
	}()

	guestMetricsCh := make(chan guestConnMetric, benchConnCount)
	var wg sync.WaitGroup
	benchWallStart := time.Now()
	for i := 0; i < benchConnCount; i++ {
		idx := i
		wg.Go(func() {
			startMu.Lock()
			startTimes[idx] = time.Now()
			startMu.Unlock()

			fut, release := debug.StartBenchmarkVsock(ctx, func(p vmapi.Debug_startBenchmarkVsock_Params) error {
				p.SetPort(benchPort)
				p.SetTotalBytes(benchTotalByte)
				p.SetChunkBytes(benchChunkByte)
				return nil
			})
			defer release()
			res, err := fut.Struct()
			if err != nil {
				guestMetricsCh <- guestConnMetric{idx: idx, err: err}
				return
			}
			guestMetricsCh <- guestConnMetric{
				idx:           idx,
				bytesPerSec:   res.BytesPerSec(),
				durationNanos: res.DurationNanos(),
			}
		})
	}
	wg.Wait()
	close(guestMetricsCh)

	hostMetrics := make([]hostConnMetric, benchConnCount)
	hostErrors := make(map[int]error)
	hostReceived := 0
	for hostReceived < benchConnCount {
		select {
		case metric := <-hostMetricsCh:
			if metric.idx < 0 || metric.idx >= benchConnCount {
				t.Fatalf("host benchmark produced invalid conn index=%d", metric.idx)
			}
			if metric.err != nil {
				hostErrors[metric.idx] = metric.err
			}
			hostMetrics[metric.idx] = metric
			hostReceived++
		case <-time.After(5 * time.Minute):
			t.Fatalf("timeout waiting for host benchmark metrics: got=%d want=%d", hostReceived, benchConnCount)
		}
	}

	guestMetrics := make([]guestConnMetric, benchConnCount)
	guestErrors := make(map[int]error)
	guestReceived := 0
	for gm := range guestMetricsCh {
		if gm.idx < 0 || gm.idx >= benchConnCount {
			t.Fatalf("guest benchmark produced invalid conn index=%d", gm.idx)
		}
		if gm.err != nil {
			guestErrors[gm.idx] = gm.err
		}
		guestMetrics[gm.idx] = gm
		guestReceived++
	}
	if guestReceived != benchConnCount {
		t.Fatalf("guest metrics count mismatch got=%d want=%d", guestReceived, benchConnCount)
	}

	benchWallDur := time.Since(benchWallStart)
	var totalHostBytes int64
	t.Logf("benchmark connections=%d port=%d bytes_per_connection=%d", benchConnCount, benchPort, benchTotalByte)
	for i := 0; i < benchConnCount; i++ {
		hm := hostMetrics[i]
		gm := guestMetrics[i]
		if err, ok := hostErrors[i]; ok {
			t.Fatalf("host benchmark failed conn=%d: %v", i, err)
		}
		if err, ok := guestErrors[i]; ok {
			t.Fatalf("guest benchmark failed conn=%d: %v", i, err)
		}
		if hm.bytes != int64(benchTotalByte) {
			t.Fatalf("host bytes mismatch conn=%d got=%d want=%d", i, hm.bytes, benchTotalByte)
		}
		totalHostBytes += hm.bytes
		hostBps := float64(hm.bytes) / hm.duration.Seconds()
		t.Logf(
			"conn=%d ttfb=%s host_bytes_per_sec=%.2f guest_bytes_per_sec=%.2f bytes=%d host_duration=%s guest_duration_nanos=%d",
			i,
			hm.ttfb,
			hostBps,
			gm.bytesPerSec,
			hm.bytes,
			hm.duration,
			gm.durationNanos,
		)
	}
	totalHostBps := float64(totalHostBytes) / benchWallDur.Seconds()
	t.Logf("benchmark total host_bytes_per_sec=%.2f total_bytes=%d duration=%s connections=%d", totalHostBps, totalHostBytes, benchWallDur, benchConnCount)

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

func TestBenchmarkContainerStartup(t *testing.T) {
	repoRoot := filepath.Clean("..")
	kernelPath := filepath.Join(repoRoot, "build", "kernel")
	rootImagePath := filepath.Join(repoRoot, "build", "rootfs.raw")
	managerPath := filepath.Join(repoRoot, "build", "vmmanager")

	for _, p := range []string{kernelPath, rootImagePath, managerPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing required artifact %q: %v (run `make image vm-binaries`)", p, err)
		}
	}

	imageRef := os.Getenv("IMAGE")
	if imageRef == "" {
		imageRef = "docker.io/library/ubuntu:latest"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	cacheDiskPath, built, err := PrepareImageExt4Disk(ctx, imageRef, filepath.Join(repoRoot, "build", "image-disks"), 4096, "vmrunner-data")
	if err != nil {
		t.Fatalf("prepare image disk: %v", err)
	}
	t.Logf("image disk path=%s built=%v", cacheDiskPath, built)

	shortTmpDir, err := os.MkdirTemp("", "vmr-bench-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(shortTmpDir)
	readySock := filepath.Join(shortTmpDir, "a.sock")
	cfg := VMConfig{
		BootMode:             BootModeLinux,
		KernelPath:           kernelPath,
		RootImage:            rootImagePath,
		MemoryMiB:            2048,
		CPUs:                 4,
		AgentVsockPort:       7000,
		AgentReadySocketPath: readySock,
		ExtraDiskPaths:       []string{cacheDiskPath},
		Verbose:              false,
	}

	runner := NewVMRunner(nil)
	runner.ManagerBinaryPath = managerPath

	vmStart := time.Now()
	vmCtx, err := runner.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("run vm: %v", err)
	}
	defer vmCtx.Close()
	bootLatency := time.Since(vmStart)

	agent := vmCtx.Agent()
	defer agent.Release()

	containerID := fmt.Sprintf("bench-%d", time.Now().UnixNano())
	specJSON, err := BuildDefaultOCISpecJSON(DefaultOCISpecOptions{
		ImageRef:   imageRef,
		RootfsPath: fmt.Sprintf("/run/vmrunner/containers/%s/rootfs", containerID),
		Entrypoint: []string{"/bin/sh", "-lc"},
		Cmd:        []string{"echo hello world"},
	})
	if err != nil {
		t.Fatalf("build default spec: %v", err)
	}

	svcFuture, svcRelease := agent.ContainerService(ctx, nil)
	svcRes, err := svcFuture.Struct()
	if err != nil {
		svcRelease()
		t.Fatalf("container service call failed: %v", err)
	}
	svc := svcRes.Service().AddRef()
	svcRelease()
	defer svc.Release()

	containerFlowStart := vmStart
	createFuture, createRelease := svc.Create(ctx, func(p vmapi.ContainerService_create_Params) error {
		if err := p.SetOci(specJSON); err != nil {
			return err
		}
		if err := p.SetImage(imageRef); err != nil {
			return err
		}
		return p.SetId(containerID)
	})
	createRes, err := createFuture.Struct()
	if err != nil {
		createRelease()
		t.Fatalf("create container failed: %v", err)
	}
	container := createRes.Container().AddRef()
	createRelease()
	defer container.Release()

	stdoutSink := &benchStreamSink{}
	stderrSink := &benchStreamSink{}
	stdoutCap := vmapi.ByteStream_ServerToClient(stdoutSink)
	defer stdoutCap.Release()
	stderrCap := vmapi.ByteStream_ServerToClient(stderrSink)
	defer stderrCap.Release()

	capnpStartCallStart := time.Now()
	startFuture, startRelease := container.Start(ctx, func(p vmapi.Container_start_Params) error {
		if err := p.SetStdout(stdoutCap); err != nil {
			return err
		}
		return p.SetStderr(stderrCap)
	})
	startRes, err := startFuture.Struct()
	containerStartedAt := time.Now()
	if err != nil {
		startRelease()
		t.Fatalf("start container failed: %v", err)
	}
	task := startRes.Task().AddRef()
	startRelease()
	defer task.Release()

	exitFuture, exitRelease := task.ExitCode(ctx, nil)
	defer exitRelease()
	exitRes, err := exitFuture.Struct()
	if err != nil {
		t.Fatalf("wait exit code failed: %v", err)
	}
	totalStartupToExit := time.Since(containerFlowStart)

	stdoutFirstAt, stdoutBytes, stdoutText := stdoutSink.snapshot()
	stderrFirstAt, stderrBytes, stderrText := stderrSink.snapshot()
	firstOutputAt := stdoutFirstAt
	if firstOutputAt.IsZero() || (!stderrFirstAt.IsZero() && stderrFirstAt.Before(firstOutputAt)) {
		firstOutputAt = stderrFirstAt
	}
	if firstOutputAt.IsZero() {
		t.Fatalf("container produced no output")
	}
	firstOutputLatency := firstOutputAt.Sub(containerFlowStart)
	fullStartToContainerStarted := containerStartedAt.Sub(vmStart)
	capnpContainerStartLatency := containerStartedAt.Sub(capnpStartCallStart)

	t.Logf("container benchmark image=%s", imageRef)
	t.Logf("container benchmark boot_latency=%s from_before_vm_start_to_container_start=%s capnp_container_start_latency=%s first_output_latency=%s total_to_exit=%s exit_code=%d stdout_bytes=%d stderr_bytes=%d",
		bootLatency, fullStartToContainerStarted, capnpContainerStartLatency, firstOutputLatency, totalStartupToExit, exitRes.Code(), stdoutBytes, stderrBytes)

	if !strings.Contains(stdoutText, "hello world") {
		t.Fatalf("expected stdout to contain %q, got stdout=%q stderr=%q", "hello world", stdoutText, stderrText)
	}
	if exitRes.Code() != 0 {
		t.Logf("container non-zero exit stderr=%q", stderrText)
		t.Logf("container non-zero exit stdout=%q", stdoutText)
		t.Fatalf("unexpected exit code: %d stdout=%q stderr=%q", exitRes.Code(), stdoutText, stderrText)
	}

	if err := vmCtx.Kill(); err != nil {
		t.Fatalf("kill vm: %v", err)
	}
}

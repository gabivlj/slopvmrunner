package vm

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sync"

	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
)

type ContainerResult struct {
	ExitCode int32
}

type linePrinterStream struct {
	mu sync.Mutex
	// Holds last partial line between chunks.
	partial bytes.Buffer
	stream  string
	enabled bool
}

func (s *linePrinterStream) Write(_ context.Context, call vmapi.ByteStream_write) error {
	chunk, err := call.Args().Chunk()
	if err != nil {
		return err
	}
	if len(chunk) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.partial.Write(chunk); err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(s.partial.Bytes()))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var consumed int
	for sc.Scan() {
		line := sc.Text()
		consumed += len(line) + 1 // include '\n'
		if s.enabled {
			fmt.Printf("[container %s] %s\n", s.stream, line)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if consumed > 0 {
		remaining := s.partial.Bytes()[consumed:]
		s.partial.Reset()
		_, _ = s.partial.Write(remaining)
	}
	return nil
}

func (s *linePrinterStream) Done(_ context.Context, _ vmapi.ByteStream_done) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partial.Len() > 0 {
		if s.enabled {
			fmt.Printf("[container %s] %s\n", s.stream, s.partial.String())
		}
		s.partial.Reset()
	}
	return nil
}

func RunContainer(ctx context.Context, agent vmapi.Agent, id, imageRef, rootfsPath, containerStateDisk string, specJSON []byte) (ContainerResult, error) {
	svcFuture, svcRelease := agent.ContainerService(ctx, nil)
	defer svcRelease()
	svcRes, err := svcFuture.Struct()
	if err != nil {
		return ContainerResult{}, err
	}
	svc := svcRes.Service()
	defer svc.Release()

	createFuture, createRelease := svc.Create(ctx, func(p vmapi.ContainerService_create_Params) error {
		if err := p.SetOci(specJSON); err != nil {
			return err
		}
		if err := p.SetImage(imageRef); err != nil {
			return err
		}
		if err := p.SetId(id); err != nil {
			return err
		}
		if err := p.SetRootfsPath(rootfsPath); err != nil {
			return err
		}
		return p.SetContainerStateDisk(containerStateDisk)
	})
	defer createRelease()
	createRes, err := createFuture.Struct()
	if err != nil {
		return ContainerResult{}, err
	}
	container := createRes.Container()
	defer container.Release()

	stdoutCollector := &linePrinterStream{stream: "stdout", enabled: true}
	stderrCollector := &linePrinterStream{stream: "stderr", enabled: true}
	stdoutCap := vmapi.ByteStream_ServerToClient(stdoutCollector)
	defer stdoutCap.Release()
	stderrCap := vmapi.ByteStream_ServerToClient(stderrCollector)
	defer stderrCap.Release()

	startFuture, startRelease := container.Start(ctx, func(p vmapi.Container_start_Params) error {
		if err := p.SetStdout(stdoutCap); err != nil {
			return err
		}
		return p.SetStderr(stderrCap)
	})
	defer startRelease()
	startRes, err := startFuture.Struct()
	if err != nil {
		return ContainerResult{}, err
	}
	task := startRes.Task()
	defer task.Release()

	exitFuture, exitRelease := task.ExitCode(ctx, nil)
	defer exitRelease()
	exitRes, err := exitFuture.Struct()
	if err != nil {
		return ContainerResult{}, err
	}

	return ContainerResult{
		ExitCode: exitRes.Code(),
	}, nil
}

// Backward-compat shim while callers migrate.
func RunOCI(ctx context.Context, agent vmapi.Agent, id, imageRef, rootfsPath, containerStateDisk string, specJSON []byte) (ContainerResult, error) {
	return RunContainer(ctx, agent, id, imageRef, rootfsPath, containerStateDisk, specJSON)
}

//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
	"syscall"
)

const (
	defaultRuncPath = "/usr/bin/runc"
)

var containerIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type containerServiceServer struct{}

type containerServer struct {
	id                 string
	imageRef           string
	ociSpec            []byte
	rootfsPath         string
	containerStateDisk string
}

type taskServer struct {
	waitOnce sync.Once
	waitErr  error
	exitCode int32
	waitFn   func() (int32, error)
	stdinW   io.WriteCloser
	cleanup  func()
}

type stdinStreamServer struct {
	w io.WriteCloser
}

func (agentServer) ContainerService(_ context.Context, call vmapi.Agent_containerService) error {
	res, err := call.AllocResults()
	if err != nil {
		return err
	}
	svc := vmapi.ContainerService_ServerToClient(containerServiceServer{})
	return res.SetService(svc)
}

func (containerServiceServer) Create(_ context.Context, call vmapi.ContainerService_create) error {
	id, err := call.Args().Id()
	if err != nil {
		return err
	}

	if !containerIDRe.MatchString(id) {
		return fmt.Errorf("invalid container id %q", id)
	}

	imageRef, err := call.Args().Image()
	if err != nil {
		return err
	}

	if strings.TrimSpace(imageRef) == "" {
		return fmt.Errorf("imageRef is required")
	}

	ociSpec, err := call.Args().Oci()
	if err != nil {
		return err
	}

	rootfsPath, err := call.Args().RootfsPath()
	if err != nil {
		return err
	}

	if strings.TrimSpace(rootfsPath) == "" {
		return fmt.Errorf("rootfsPath is required")
	}

	containerStateDisk, err := call.Args().ContainerStateDisk()
	if err != nil {
		return err
	}

	if strings.TrimSpace(containerStateDisk) == "" {
		return fmt.Errorf("containerStateDisk is required")
	}

	if len(ociSpec) == 0 {
		return fmt.Errorf("oci spec is empty")
	}

	res, err := call.AllocResults()
	if err != nil {
		return err
	}

	container := vmapi.Container_ServerToClient(&containerServer{
		id:                 id,
		imageRef:           imageRef,
		ociSpec:            append([]byte(nil), ociSpec...),
		rootfsPath:         rootfsPath,
		containerStateDisk: containerStateDisk,
	})

	return res.SetContainer(container)
}

func (c *containerServer) Start(_ context.Context, call vmapi.Container_start) error {
	if virtioFSEnabled() {
		// In virtiofs mode bundle/state live on the writable state disk mount.
		// Mount it before creating bundle/config so files aren't hidden by a later mount.
		if _, err := ensureOverlayStateMounted(c.containerStateDisk); err != nil {
			return err
		}
	}

	bundleDir := filepath.Join(c.containerStateDisk, c.id, "bundle")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return fmt.Errorf("create bundle dir %q: %w", bundleDir, err)
	}

	configPath := filepath.Join(bundleDir, "config.json")
	if err := os.WriteFile(configPath, c.ociSpec, 0o644); err != nil {
		return fmt.Errorf("write OCI spec: %w", err)
	}

	if err := bindImageRootfs(c.id, bundleDir, c.rootfsPath, c.containerStateDisk); err != nil {
		return err
	}

	runcPath := os.Getenv("AGENT_RUNC_PATH")
	if runcPath == "" {
		runcPath = defaultRuncPath
	}

	cmd := exec.Command(runcPath, "run", "--bundle", bundleDir, c.id)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Get signals from parent.
		Setpgid: true,
	}

	stdinW, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderrR, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start runc: %w", err)
	}

	stdout := call.Args().Stdout().AddRef()
	stderr := call.Args().Stderr().AddRef()

	go pumpToByteStream(stdoutR, stdout)
	go pumpToByteStream(stderrR, stderr)

	task := &taskServer{
		stdinW: stdinW,
		waitFn: func() (int32, error) {
			if taskErr := cmd.Wait(); taskErr == nil {
				return 0, nil
			} else {
				var exitErr *exec.ExitError
				if !asExitError(taskErr, &exitErr) {
					return -1, taskErr
				}

				return int32(exitErr.ExitCode()), nil
			}
		},
		cleanup: func() {
			_ = syscall.Unmount(filepath.Join(bundleDir, "rootfs"), 0)
		},
	}

	res, err := call.AllocResults()
	if err != nil {
		return err
	}

	return res.SetTask(vmapi.Task_ServerToClient(task))
}

func (t *taskServer) Stdin(_ context.Context, call vmapi.Task_stdin) error {
	res, err := call.AllocResults()
	if err != nil {
		return err
	}
	stream := vmapi.ByteStream_ServerToClient(&stdinStreamServer{w: t.stdinW})
	return res.SetStream(stream)
}

func (t *taskServer) ExitCode(_ context.Context, call vmapi.Task_exitCode) error {
	t.waitOnce.Do(func() {
		t.exitCode, t.waitErr = t.waitFn()
		if t.cleanup != nil {
			t.cleanup()
		}
	})
	if t.waitErr != nil {
		return fmt.Errorf("wait container: %w", t.waitErr)
	}
	res, err := call.AllocResults()
	if err != nil {
		return err
	}
	res.SetCode(t.exitCode)
	return nil
}

func (s *stdinStreamServer) Write(_ context.Context, call vmapi.ByteStream_write) error {
	chunk, err := call.Args().Chunk()
	if err != nil {
		return err
	}
	if len(chunk) == 0 {
		return nil
	}
	_, err = s.w.Write(chunk)
	return err
}

func (s *stdinStreamServer) Done(_ context.Context, _ vmapi.ByteStream_done) error {
	if s.w == nil {
		return nil
	}
	return s.w.Close()
}

func asExitError(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = exitErr
	return true
}

func bindImageRootfs(containerID, bundleDir, rootfsPath, containerStateDisk string) error {
	return mountOverlayFromRootFS(containerID, bundleDir, rootfsPath, containerStateDisk)
}

func mountOverlayFromRootFS(containerID, bundleDir, lower, containerStateDisk string) error {
	info, err := os.Stat(lower)
	if err != nil {
		return fmt.Errorf("lowerdir not found at %s: %w", lower, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("lowerdir is not directory: %s", lower)
	}

	stateMount, err := ensureOverlayStateMounted(containerStateDisk)
	if err != nil {
		return err
	}

	runRoot := filepath.Join(stateMount, containerID)
	overlaysRoot := filepath.Join(runRoot, "overlays")
	upper := filepath.Join(overlaysRoot, "diff")
	work := filepath.Join(overlaysRoot, "work")
	merged := filepath.Join(bundleDir, "rootfs")
	if err := os.MkdirAll(upper, 0o755); err != nil {
		return err
	}

	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}

	if err := os.MkdirAll(merged, 0o755); err != nil {
		return err
	}

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lower, upper, work)
	if err := syscall.Mount("overlay", merged, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay lower=%s upper=%s work=%s merged=%s: %w", lower, upper, work, merged, err)
	}

	return nil
}

func ensureOverlayStateMounted(mountPoint string) (string, error) {
	device := containerWritableDiskDevice()
	if strings.TrimSpace(mountPoint) == "" {
		mountPoint = overlayStateMountPointFS()
	}

	if _, err := os.Stat(device); err != nil {
		return "", fmt.Errorf("overlay state disk device not found: %s (%w)", device, err)
	}

	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return "", fmt.Errorf("create overlay mountpoint %s: %w", mountPoint, err)
	}

	if err := syscall.Mount(device, mountPoint, "ext4", 0, ""); err != nil && err != syscall.EBUSY {
		return "", fmt.Errorf("mount overlay state disk %s at %s: %w", device, mountPoint, err)
	}

	return mountPoint, nil
}

func pumpToByteStream(src io.ReadCloser, dst vmapi.ByteStream) {
	defer src.Close()
	defer dst.Release()

	buf := make([]byte, 64*1024)
	ctx := context.Background()

	for {
		n, err := src.Read(buf)
		if n > 0 {
			callErr := dst.Write(ctx, func(p vmapi.ByteStream_write_Params) error {
				return p.SetChunk(buf[:n])
			})
			if callErr != nil {
				log.Printf("stream write failed: %v", callErr)
				return
			}
		}

		if err == io.EOF {
			break
		}

		if errors.Is(err, os.ErrClosed) {
			return
		}

		if err != nil {
			log.Printf("stream read failed: %v", err)
			return
		}
	}

	fut, rel := dst.Done(ctx, nil)
	_, _ = fut.Struct()
	rel()
}

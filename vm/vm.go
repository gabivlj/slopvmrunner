package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"capnproto.org/go/capnp/v3/rpc"
	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
)

const (
	BootModeLinux = "linux"
	BootModeEFI   = "efi"
)

type VMConfig struct {
	BootMode             string
	KernelPath           string
	InitrdPath           string
	RootImage            string
	MemoryMiB            int
	CPUs                 int
	AgentVsockPort       int
	AgentReadySocketPath string
	EnableNetwork        bool
	VMNetworkCIDR        string
	VMNetworkGateway     string
	VMNetworkIfName      string
	Verbose              bool
}

func (c VMConfig) Validate() error {
	if c.RootImage == "" {
		return errors.New("root image is required")
	}
	switch c.BootMode {
	case "", BootModeLinux, BootModeEFI:
	default:
		return fmt.Errorf("invalid boot mode %q (expected %q or %q)", c.BootMode, BootModeLinux, BootModeEFI)
	}
	if c.BootMode == BootModeLinux && c.KernelPath == "" {
		return errors.New("kernel path is required in linux boot mode")
	}
	if c.MemoryMiB <= 0 {
		return errors.New("memory MiB must be > 0")
	}
	if c.CPUs <= 0 {
		return errors.New("cpus must be > 0")
	}
	if c.AgentVsockPort <= 0 {
		return errors.New("agent vsock port must be > 0")
	}
	if c.AgentReadySocketPath == "" {
		return errors.New("agent readiness unix socket path is required")
	}
	if c.EnableNetwork {
		if c.VMNetworkCIDR != "" {
			if _, err := netip.ParsePrefix(c.VMNetworkCIDR); err != nil {
				return fmt.Errorf("invalid vm network cidr %q: %w", c.VMNetworkCIDR, err)
			}
			if c.VMNetworkIfName == "" {
				return errors.New("vm network ifname is required when vm network cidr is set")
			}
		}
		if c.VMNetworkGateway != "" {
			if _, err := netip.ParseAddr(c.VMNetworkGateway); err != nil {
				return fmt.Errorf("invalid vm network gateway %q: %w", c.VMNetworkGateway, err)
			}
		}
	}
	return nil
}

func (c VMConfig) ManagerArgs() ([]string, error) {
	if c.BootMode == "" {
		c.BootMode = BootModeLinux
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}

	args := []string{
		"--boot-mode", c.BootMode,
		"--root-image", c.RootImage,
		"--memory-mib", strconv.Itoa(c.MemoryMiB),
		"--cpus", strconv.Itoa(c.CPUs),
		"--agent-vsock-port", strconv.Itoa(c.AgentVsockPort),
		"--agent-ready-socket", c.AgentReadySocketPath,
		"--enable-network", strconv.FormatBool(c.EnableNetwork),
	}
	if c.VMNetworkCIDR != "" {
		args = append(args, "--vm-network-cidr", c.VMNetworkCIDR)
	}
	if c.VMNetworkGateway != "" {
		args = append(args, "--vm-network-gateway", c.VMNetworkGateway)
	}
	if c.VMNetworkIfName != "" {
		args = append(args, "--vm-network-ifname", c.VMNetworkIfName)
	}

	if c.BootMode == BootModeLinux {
		args = append(args, "--kernel", c.KernelPath)
	}
	if c.InitrdPath != "" {
		args = append(args, "--initrd", c.InitrdPath)
	}
	if c.Verbose {
		args = append(args, "--verbose")
	}

	return args, nil
}

type VMRunner struct {
	ManagerBinaryPath string
	Logger            *slog.Logger
}

type VMContext struct {
	waitCh    chan error
	killFn    func() error
	agent     vmapi.Agent
	rpcConn   *rpc.Conn
	agentRWC  *os.File
	logger    *slog.Logger
	closeOnce sync.Once
}

func NewVMRunner(logger *slog.Logger) *VMRunner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &VMRunner{
		ManagerBinaryPath: "build/vmmanager",
		Logger:            logger,
	}
}

func (v *VMContext) WaitCh() <-chan error {
	return v.waitCh
}

func (v *VMContext) Kill() error {
	return v.killFn()
}

func (v *VMContext) Agent() vmapi.Agent {
	return v.agent.AddRef()
}

func (v *VMContext) Close() {
	v.closeOnce.Do(func() {
		if v.agent.IsValid() {
			v.agent.Release()
		}
		if v.rpcConn != nil {
			_ = v.rpcConn.Close()
		}
		if v.agentRWC != nil {
			_ = v.agentRWC.Close()
		}
	})
}

func (r *VMRunner) Command(ctx context.Context, cfg VMConfig) (*exec.Cmd, error) {
	args, err := cfg.ManagerArgs()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(r.ManagerBinaryPath); err != nil {
		return nil, fmt.Errorf("manager binary not found at %q: %w", r.ManagerBinaryPath, err)
	}
	cmd := exec.CommandContext(ctx, r.ManagerBinaryPath, args...)
	return cmd, nil
}

func (r *VMRunner) Run(ctx context.Context, cfg VMConfig) (*VMContext, error) {
	cmd, err := r.Command(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.AgentReadySocketPath), 0o755); err != nil {
		return nil, fmt.Errorf("create readiness socket directory: %w", err)
	}

	_ = os.Remove(cfg.AgentReadySocketPath)
	readyListener, err := net.Listen("unix", cfg.AgentReadySocketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on readiness socket %q: %w", cfg.AgentReadySocketPath, err)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	startAt := time.Now()
	if err := cmd.Start(); err != nil {
		_ = readyListener.Close()
		_ = os.Remove(cfg.AgentReadySocketPath)
		return nil, err
	}

	readyCh := make(chan time.Duration, 1)
	readyErrCh := make(chan error, 1)
	agentRWCCh := make(chan *os.File, 1)
	go func() {
		conn, err := readyListener.Accept()

		if err != nil {
			readyErrCh <- err
			return
		}
		defer conn.Close()

		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			readyErrCh <- fmt.Errorf("unexpected readiness connection type %T", conn)
			return
		}

		fd, err := recvFD(unixConn)
		if err != nil {
			readyErrCh <- fmt.Errorf("recv fd: %w", err)
			return
		}

		file := os.NewFile(uintptr(fd), "agent-vsock")
		if file == nil {
			_ = syscall.Close(fd)
			readyErrCh <- errors.New("failed to wrap received fd")
			return
		}
		agentRWCCh <- file
		readyCh <- time.Since(startAt)
	}()

	procWaitCh := make(chan error, 1)
	go func() { procWaitCh <- cmd.Wait() }()

	ready := false
	var agentRWC *os.File
	for !ready {
		select {
		case f := <-agentRWCCh:
			agentRWC = f
		case d := <-readyCh:
			r.Logger.Info("agent readiness via vsock", "latency", d.Round(time.Millisecond).String())
			ready = true
		case err := <-readyErrCh:
			_ = cmd.Process.Kill()
			<-procWaitCh
			_ = readyListener.Close()
			_ = os.Remove(cfg.AgentReadySocketPath)
			return nil, fmt.Errorf("readiness unix socket accept failed: %w", err)
		case err := <-procWaitCh:
			_ = readyListener.Close()
			_ = os.Remove(cfg.AgentReadySocketPath)
			return nil, fmt.Errorf("vm manager exited before agent readiness: %w", err)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-procWaitCh
			_ = readyListener.Close()
			_ = os.Remove(cfg.AgentReadySocketPath)
			return nil, ctx.Err()
		}
	}
	_ = readyListener.Close()
	_ = os.Remove(cfg.AgentReadySocketPath)

	rpcConn := rpc.NewConn(rpc.NewStreamTransport(agentRWC), nil)
	agent := vmapi.Agent(rpcConn.Bootstrap(ctx))
	if !agent.IsValid() {
		_ = rpcConn.Close()
		_ = agentRWC.Close()
		_ = cmd.Process.Kill()
		<-procWaitCh
		return nil, errors.New("received invalid agent capability")
	}

	vmCtx := &VMContext{
		waitCh: make(chan error, 1),
		killFn: func() error {
			if cmd.Process == nil {
				return errors.New("vm process is not running")
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
			return nil
		},
		agent:    agent,
		rpcConn:  rpcConn,
		agentRWC: agentRWC,
		logger:   r.Logger,
	}

	go func() {
		err := <-procWaitCh
		if err != nil {
			r.Logger.Error("vm manager exited", "error", err)
		} else {
			r.Logger.Info("vm manager exited")
		}
		vmCtx.Close()
		vmCtx.waitCh <- err
		close(vmCtx.waitCh)
	}()

	return vmCtx, nil
}

func recvFD(conn *net.UnixConn) (int, error) {
	data := make([]byte, 1)
	oob := make([]byte, 128)
	_, oobn, _, _, err := conn.ReadMsgUnix(data, oob)
	if err != nil {
		return -1, err
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return -1, err
	}
	for _, m := range msgs {
		fds, err := syscall.ParseUnixRights(&m)
		if err != nil {
			continue
		}
		if len(fds) > 0 {
			return fds[0], nil
		}
	}
	return -1, errors.New("no fd in unix rights message")
}

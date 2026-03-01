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
	"strings"
	"sync"
	"syscall"
	"time"

	"capnproto.org/go/capnp/v3/rpc"
	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
)

type BootMode string

const (
	BootModeLinux BootMode = "linux"
	BootModeEFI   BootMode = "efi"
)

func (m BootMode) String() string {
	return string(m)
}

func (m BootMode) IsValid() bool {
	return m == BootModeLinux || m == BootModeEFI
}

func ParseBootMode(raw string) (BootMode, error) {
	switch BootMode(strings.ToLower(strings.TrimSpace(raw))) {
	case BootModeLinux:
		return BootModeLinux, nil
	case BootModeEFI:
		return BootModeEFI, nil
	default:
		return "", fmt.Errorf("invalid boot mode %q (expected %q or %q)", raw, BootModeLinux, BootModeEFI)
	}
}

type NetworkMode string

const (
	NetworkModeNAT      NetworkMode = "nat"
	NetworkModeBridged  NetworkMode = "bridged"
	NetworkModeHostOnly NetworkMode = "hostonly"
)

func (m NetworkMode) String() string {
	return string(m)
}

func (m NetworkMode) IsValid() bool {
	return m == NetworkModeNAT || m == NetworkModeBridged || m == NetworkModeHostOnly
}

func ParseNetworkMode(raw string) (NetworkMode, error) {
	switch NetworkMode(strings.ToLower(strings.TrimSpace(raw))) {
	case NetworkModeNAT:
		return NetworkModeNAT, nil
	case NetworkModeBridged:
		return NetworkModeBridged, nil
	case NetworkModeHostOnly:
		return NetworkModeHostOnly, nil
	default:
		return "", fmt.Errorf("invalid network mode %q (expected %q, %q, or %q)", raw, NetworkModeNAT, NetworkModeBridged, NetworkModeHostOnly)
	}
}

type VMConfig struct {
	BootMode             BootMode
	KernelPath           string
	InitrdPath           string
	RootImage            string
	ExtraDiskPaths       []string
	MemoryMiB            int
	CPUs                 int
	AgentVsockPort       int
	ProxyVsockPorts      []int
	AgentReadySocketPath string
	EnableNetwork        bool
	NetworkMode          NetworkMode
	BridgeInterface      string
	VMNetworkCIDR        string
	VMNetworkGateway     string
	VMNetworkIfName      string
	Verbose              bool
}

func (c VMConfig) Validate() error {
	if c.RootImage == "" {
		return errors.New("root image is required")
	}
	for _, diskPath := range c.ExtraDiskPaths {
		if strings.TrimSpace(diskPath) == "" {
			return errors.New("extra disk path cannot be empty")
		}
	}
	if c.BootMode != "" && !c.BootMode.IsValid() {
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
	for _, p := range c.ProxyVsockPorts {
		if p <= 0 {
			return fmt.Errorf("proxy vsock port must be > 0: %d", p)
		}
	}
	if c.AgentReadySocketPath == "" {
		return errors.New("agent readiness unix socket path is required")
	}
	if c.EnableNetwork {
		if c.NetworkMode != "" && !c.NetworkMode.IsValid() {
			return fmt.Errorf("invalid network mode %q (expected %q, %q, or %q)", c.NetworkMode, NetworkModeNAT, NetworkModeBridged, NetworkModeHostOnly)
		}
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
	if c.NetworkMode == "" {
		c.NetworkMode = NetworkModeHostOnly
	}

	args := []string{
		"--boot-mode", c.BootMode.String(),
		"--root-image", c.RootImage,
		"--memory-mib", strconv.Itoa(c.MemoryMiB),
		"--cpus", strconv.Itoa(c.CPUs),
		"--agent-vsock-port", strconv.Itoa(c.AgentVsockPort),
		"--agent-ready-socket", c.AgentReadySocketPath,
		"--enable-network", strconv.FormatBool(c.EnableNetwork),
		"--network-mode", c.NetworkMode.String(),
	}
	for _, diskPath := range c.ExtraDiskPaths {
		args = append(args, "--extra-disk", diskPath)
	}
	for _, port := range c.ProxyVsockPorts {
		args = append(args, "--proxy-vsock-port", strconv.Itoa(port))
	}
	if c.BridgeInterface != "" {
		args = append(args, "--bridge-interface", c.BridgeInterface)
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
	proxyCh   chan ProxyConn
	logger    *slog.Logger
	closeOnce sync.Once
}

type ProxyConn struct {
	Port uint32
	File *os.File
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

func (v *VMContext) ProxyConnCh() <-chan ProxyConn {
	return v.proxyCh
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
	acceptCh := make(chan ProxyConn, 32)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptCh)
		defer close(acceptDone)
		for {
			conn, err := readyListener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				select {
				case readyErrCh <- err:
				default:
				}
				return
			}

			unixConn, ok := conn.(*net.UnixConn)
			if !ok {
				_ = conn.Close()
				select {
				case readyErrCh <- fmt.Errorf("unexpected readiness connection type %T", conn):
				default:
				}
				return
			}

			fd, port, err := recvFD(unixConn)
			_ = conn.Close()
			if err != nil {
				select {
				case readyErrCh <- fmt.Errorf("recv fd: %w", err):
				default:
				}
				return
			}
			file := os.NewFile(uintptr(fd), fmt.Sprintf("vsock-%d", port))
			if file == nil {
				_ = syscall.Close(fd)
				select {
				case readyErrCh <- errors.New("failed to wrap received fd"):
				default:
				}
				return
			}
			acceptCh <- ProxyConn{
				Port: port,
				File: file,
			}
		}
	}()

	procWaitCh := make(chan error, 1)
	go func() { procWaitCh <- cmd.Wait() }()

	ready := false
	var agentRWC *os.File
	pendingProxyConns := make([]ProxyConn, 0, 8)
	agentPort := uint32(cfg.AgentVsockPort)
	for !ready {
		select {
		case accepted, ok := <-acceptCh:
			if !ok {
				return nil, errors.New("readiness channel closed before agent connection")
			}
			if accepted.Port == agentPort && agentRWC == nil {
				agentRWC = accepted.File
				readyCh <- time.Since(startAt)
				continue
			}
			pendingProxyConns = append(pendingProxyConns, accepted)
		case d := <-readyCh:
			r.Logger.Info("agent readiness via vsock", "latency", d.Round(time.Millisecond).String(), "port", agentPort)
			ready = agentRWC != nil
		case err := <-readyErrCh:
			_ = cmd.Process.Kill()
			<-procWaitCh
			_ = readyListener.Close()
			<-acceptDone
			_ = os.Remove(cfg.AgentReadySocketPath)
			return nil, fmt.Errorf("readiness unix socket accept failed: %w", err)
		case err := <-procWaitCh:
			_ = readyListener.Close()
			<-acceptDone
			_ = os.Remove(cfg.AgentReadySocketPath)
			return nil, fmt.Errorf("vm manager exited before agent readiness: %w", err)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-procWaitCh
			_ = readyListener.Close()
			<-acceptDone
			_ = os.Remove(cfg.AgentReadySocketPath)
			return nil, ctx.Err()
		}
	}

	rpcConn := rpc.NewConn(rpc.NewStreamTransport(agentRWC), nil)
	agent := vmapi.Agent(rpcConn.Bootstrap(ctx))
	if !agent.IsValid() {
		_ = rpcConn.Close()
		_ = agentRWC.Close()
		_ = cmd.Process.Kill()
		<-procWaitCh
		_ = readyListener.Close()
		<-acceptDone
		_ = os.Remove(cfg.AgentReadySocketPath)
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
		proxyCh:  make(chan ProxyConn, 64),
		logger:   r.Logger,
	}
	for _, accepted := range pendingProxyConns {
		vmCtx.proxyCh <- accepted
	}
	go func() {
		for accepted := range acceptCh {
			vmCtx.proxyCh <- accepted
		}
		close(vmCtx.proxyCh)
	}()

	go func() {
		err := <-procWaitCh
		_ = readyListener.Close()
		<-acceptDone
		_ = os.Remove(cfg.AgentReadySocketPath)
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

func recvFD(conn *net.UnixConn) (int, uint32, error) {
	data := make([]byte, 4)
	oob := make([]byte, 128)
	n, oobn, _, _, err := conn.ReadMsgUnix(data, oob)
	if err != nil {
		return -1, 0, err
	}
	if n < 4 {
		return -1, 0, fmt.Errorf("readiness payload too short: %d", n)
	}
	port := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return -1, 0, err
	}
	for _, m := range msgs {
		fds, err := syscall.ParseUnixRights(&m)
		if err != nil {
			continue
		}
		if len(fds) > 0 {
			return fds[0], port, nil
		}
	}
	return -1, port, errors.New("no fd in unix rights message")
}

package main

import (
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	capnp "capnproto.org/go/capnp/v3"
	"capnproto.org/go/capnp/v3/rpc"
	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
)

func main() {
	if os.Getpid() == 1 {
		go reapZombies()
	}
	go handleSignals()

	cid, port := parseVsockTarget()
	log.Printf("agent[%d] connecting to host via vsock cid=%d port=%d", os.Getpid(), cid, port)

	for {
		file, err := dialVsock(cid, port)
		if err != nil {
			log.Printf("vsock connect cid=%d port=%d failed: %v", cid, port, err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		log.Printf("vsock connected to host cid=%d port=%d", cid, port)

		if err := serveRPC(file); err != nil {
			log.Printf("rpc connection terminated: %v", err)
			time.Sleep(200 * time.Millisecond)
		} else {
			log.Printf("rpc connection closed; retrying")
		}
	}
}

type agentServer struct{}

func serveRPC(rwc io.ReadWriteCloser) error {
	defer rwc.Close()

	bootstrap := vmapi.Agent_ServerToClient(agentServer{})
	defer bootstrap.Release()

	rpcConn := rpc.NewConn(rpc.NewStreamTransport(rwc), &rpc.Options{
		BootstrapClient: capnp.Client(bootstrap),
	})
	defer rpcConn.Close()

	<-rpcConn.Done()
	return rpcConn.Close()
}

func parseVsockTarget() (uint32, uint32) {
	cid := uint32(2)
	port := uint32(7000)

	if env := os.Getenv("AGENT_VSOCK_CID"); env != "" {
		if p, err := strconv.Atoi(env); err == nil {
			cid = uint32(p)
		}
	}
	if env := os.Getenv("AGENT_VSOCK_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil {
			port = uint32(p)
		}
	}

	for _, arg := range readKernelCmdline() {
		if strings.HasPrefix(arg, "agent.vsock_cid=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "agent.vsock_cid=")); err == nil {
				cid = uint32(p)
			}
			continue
		}

		if strings.HasPrefix(arg, "agent.vsock_port=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "agent.vsock_port=")); err == nil {
				port = uint32(p)
			}
		}
	}

	return cid, port
}

func readKernelCmdline() []string {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return append([]string(nil), os.Args...)
	}
	return strings.Fields(string(data))
}

func reapZombies() {
	for {
		var status syscall.WaitStatus
		_, err := syscall.Wait4(-1, &status, 0, nil)
		if err != nil && err != syscall.EINTR {
			return
		}
	}
}

func handleSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	for sig := range sigCh {
		if os.Getpid() == 1 {
			_ = exec.Command("sync").Run()
		}
		log.Printf("received signal: %s", sig)
		os.Exit(0)
	}
}

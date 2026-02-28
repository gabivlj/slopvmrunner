package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	if os.Getpid() == 1 {
		go reapZombies()
	}
	go handleSignals()

	port := parsePort()
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}
	log.Printf("agent[%d] listening on %s", os.Getpid(), addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			_, _ = c.Write([]byte("hello world\n"))
		}(conn)
	}
}

func parsePort() int {
	port := 8080

	if env := os.Getenv("AGENT_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil {
			port = p
		}
	}

	for _, arg := range readKernelCmdline() {
		if strings.HasPrefix(arg, "agent.port=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "agent.port=")); err == nil {
				port = p
			}
		}
	}

	return port
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

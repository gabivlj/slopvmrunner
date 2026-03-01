package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
)

type networkServer struct{}

func (agentServer) Network(_ context.Context, call vmapi.Agent_network) error {
	res, err := call.AllocResults()
	if err != nil {
		return err
	}
	network := vmapi.Network_ServerToClient(networkServer{})
	return res.SetNetwork(network)
}

func (networkServer) ConfigureInterface(_ context.Context, call vmapi.Network_configureInterface) error {
	ifName, err := call.Args().IfName()
	if err != nil {
		return err
	}
	cidr, err := call.Args().Cidr()
	if err != nil {
		return err
	}
	gateway, err := call.Args().Gateway()
	if err != nil {
		return err
	}

	if err := configureInterfaceLink(ifName, cidr, gateway); err != nil {
		return err
	}
	log.Printf("network configured if=%s cidr=%s gateway=%s", ifName, cidr, gateway)
	probeGatewayDial(gateway)
	return nil
}

func (networkServer) SetupVsockProxy(_ context.Context, call vmapi.Network_setupVsockProxy) error {
	port := call.Args().Port()
	if port == 0 {
		return fmt.Errorf("proxy vsock port must be > 0")
	}
	log.Printf("setupVsockProxy stub called port=%d", port)
	return nil
}

func probeGatewayDial(gateway string) {
	if gateway == "" {
		return
	}
	port := os.Getenv("AGENT_GATEWAY_DIAL_PORT")
	if port == "" {
		port = "8080"
	}
	target := net.JoinHostPort(gateway, port)
	dialTimeout := 2 * time.Second
	log.Printf("temporary gateway dial trying target=%s timeout=%s", target, dialTimeout)
	d := net.Dialer{Timeout: dialTimeout}
	dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	conn, err := d.DialContext(dialCtx, "tcp", target)
	if err != nil {
		log.Printf("temporary gateway dial failed target=%s err=%v", target, err)
		return
	}
	_ = conn.Close()
	log.Printf("temporary gateway dial ok target=%s", target)
}

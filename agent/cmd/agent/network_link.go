//go:build linux

package main

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

func configureInterfaceLink(ifName, cidr, gateway string) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("lookup link %q: %w", ifName, err)
	}

	addr, err := netlink.ParseAddr(cidr)
	if err != nil {
		return fmt.Errorf("parse cidr %q: %w", cidr, err)
	}
	if err := netlink.AddrReplace(link, addr); err != nil {
		return fmt.Errorf("set address %q on %q: %w", cidr, ifName, err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("set link up %q: %w", ifName, err)
	}

	lo, err := netlink.LinkByName("lo")
	if err == nil {
		_ = netlink.LinkSetUp(lo)
	}

	gwIP := net.ParseIP(gateway)
	if gwIP == nil {
		return fmt.Errorf("parse gateway %q", gateway)
	}

	route := netlink.Route{
		LinkIndex: link.Attrs().Index,
		Gw:        gwIP,
	}
	if err := netlink.RouteReplace(&route); err != nil {
		return fmt.Errorf("set default route via %q dev %q: %w", gateway, ifName, err)
	}

	return nil
}

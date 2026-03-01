//go:build !linux

package main

import "fmt"

func configureInterfaceLink(_, _, _ string) error {
	return fmt.Errorf("network configuration is only supported on linux")
}

//go:build !linux

package main

import (
	"fmt"
	"os"
)

func dialVsock(_ uint32, _ uint32) (*os.File, error) {
	return nil, fmt.Errorf("vsock dial is only supported on linux")
}

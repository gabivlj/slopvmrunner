//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

const sizeofSockaddrVM = 0x10

type rawSockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Flags     uint8
	Zero      [3]uint8
}

func dialVsock(cid uint32, port uint32) (*os.File, error) {
	fd, err := syscall.Socket(syscall.AF_VSOCK, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}

	addr := rawSockaddrVM{
		Family: syscall.AF_VSOCK,
		Port:   port,
		CID:    cid,
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&addr)),
		uintptr(sizeofSockaddrVM),
	)
	if errno != 0 {
		_ = syscall.Close(fd)
		return nil, errno
	}

	return os.NewFile(uintptr(fd), "agent-vsock"), nil
}

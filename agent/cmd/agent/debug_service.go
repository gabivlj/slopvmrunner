//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	vmapi "github.com/gabrielvillalongasimon/vmrunner/api/gen/go/capnp"
)

type debugServer struct{}

type byteStreamServer struct {
	bytes atomic.Uint64
}

func (agentServer) Debug(_ context.Context, call vmapi.Agent_debug) error {
	res, err := call.AllocResults()
	if err != nil {
		return err
	}

	debug := vmapi.Debug_ServerToClient(debugServer{})
	return res.SetDebug(debug)
}

func (debugServer) Ping(_ context.Context, call vmapi.Debug_ping) error {
	res, err := call.AllocResults()
	if err != nil {
		return err
	}
	return res.SetMessage_("pong")
}

func (debugServer) OpenByteStream(_ context.Context, call vmapi.Debug_openByteStream) error {
	res, err := call.AllocResults()
	if err != nil {
		return err
	}
	stream := vmapi.ByteStream_ServerToClient(&byteStreamServer{})
	return res.SetStream(stream)
}

func (debugServer) StartBenchmarkVsock(_ context.Context, call vmapi.Debug_startBenchmarkVsock) error {
	call.Go()
	port := call.Args().Port()
	totalBytes := call.Args().TotalBytes()
	chunkBytes := call.Args().ChunkBytes()
	if port == 0 {
		return fmt.Errorf("startBenchmarkVsock: port must be > 0")
	}
	if totalBytes == 0 {
		return fmt.Errorf("startBenchmarkVsock: totalBytes must be > 0")
	}
	if chunkBytes == 0 {
		chunkBytes = 64 * 1024
	}

	log.Printf("startBenchmarkVsock begin port=%d totalBytes=%d chunkBytes=%d", port, totalBytes, chunkBytes)
	conn, err := dialVsock(2, port)
	if err != nil {
		return fmt.Errorf("startBenchmarkVsock dial cid=2 port=%d: %w", port, err)
	}
	defer conn.Close()

	buf := make([]byte, int(chunkBytes))
	start := time.Now()
	var wrote uint64
	for wrote < totalBytes {
		remaining := totalBytes - wrote
		toWrite := len(buf)
		if remaining < uint64(toWrite) {
			toWrite = int(remaining)
		}

		n, err := conn.Write(buf[:toWrite])
		if n > 0 {
			wrote += uint64(n)
		}
		if err != nil {
			return fmt.Errorf("startBenchmarkVsock write failed after %d bytes: %w", wrote, err)
		}
		if n == 0 {
			return fmt.Errorf("startBenchmarkVsock write returned 0 after %d bytes", wrote)
		}
	}

	duration := time.Since(start)
	var bps float64
	if duration > 0 {
		bps = float64(wrote) / duration.Seconds()
	}
	log.Printf("startBenchmarkVsock done port=%d bytes=%d duration=%s bytes_per_sec=%.2f", port, wrote, duration, bps)

	res, err := call.AllocResults()
	if err != nil {
		return err
	}
	res.SetBytesPerSec(bps)
	res.SetDurationNanos(uint64(duration.Nanoseconds()))
	return nil
}

func (s *byteStreamServer) Write(_ context.Context, call vmapi.ByteStream_write) error {
	chunk, err := call.Args().Chunk()
	if err != nil {
		return err
	}
	s.bytes.Add(uint64(len(chunk)))
	return nil
}

func (s *byteStreamServer) Done(_ context.Context, _ vmapi.ByteStream_done) error {
	log.Printf("byte stream done: bytes=%d", s.bytes.Load())
	return nil
}

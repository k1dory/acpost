//go:build unix

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// readFD reads from a terminal's descriptor, past interruptions by signals.
func readFD(fd int, b []byte) (int, error) {
	for {
		n, err := syscall.Read(fd, b)
		if err != syscall.EINTR {
			return n, err
		}
	}
}

// onResize calls fn each time the terminal's window changes its size, until ctx ends.
func onResize(ctx context.Context, fn func()) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-winch:
			fn()
		}
	}
}

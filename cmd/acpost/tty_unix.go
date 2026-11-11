//go:build unix

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
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

// termSize is the size of the terminal this command runs in.
func termSize() (cols, rows int, err error) { return term.GetSize(int(os.Stdin.Fd())) }

// vtOutput makes the terminal take what a remote shell writes as a terminal's bytes, and returns
// what puts it back: nothing to do here, a terminal does.
func vtOutput() func() { return func() {} }

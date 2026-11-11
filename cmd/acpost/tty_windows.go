package main

import (
	"context"
	"os"
	"syscall"
	"time"

	"golang.org/x/term"
)

// readFD reads from a console's handle.
func readFD(fd int, b []byte) (int, error) { return syscall.Read(syscall.Handle(fd), b) }

// onResize calls fn each time the console's window changes its size, until ctx ends. Windows
// sends no signal for it: the size is looked at.
func onResize(ctx context.Context, fn func()) {
	fd := int(os.Stdin.Fd())
	w, h, _ := term.GetSize(fd)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if nw, nh, err := term.GetSize(fd); err == nil && (nw != w || nh != h) {
				w, h = nw, nh
				fn()
			}
		}
	}
}

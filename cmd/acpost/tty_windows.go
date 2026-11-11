package main

import (
	"context"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// readFD reads from a console's handle.
func readFD(fd int, b []byte) (int, error) { return syscall.Read(syscall.Handle(fd), b) }

// termSize is the size of the console this command runs in: asked of its screen (standard
// output), which is what has one; the input handle does not.
func termSize() (cols, rows int, err error) { return term.GetSize(int(os.Stdout.Fd())) }

// vtOutput makes the console take what a remote shell writes as a terminal's bytes (escape
// sequences applied, not printed), and returns what puts its mode back. A console that takes
// them already, or an output that is no console, is left as it is.
func vtOutput() func() {
	out := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(out, &mode) != nil {
		return func() {}
	}
	want := mode | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if want == mode || windows.SetConsoleMode(out, want) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(out, mode) }
}

// onResize calls fn each time the console's window changes its size, until ctx ends. Windows
// sends no signal for it: the size is looked at.
func onResize(ctx context.Context, fn func()) {
	w, h, _ := termSize()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if nw, nh, err := termSize(); err == nil && (nw != w || nh != h) {
				w, h = nw, nh
				fn()
			}
		}
	}
}

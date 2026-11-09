package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// The hidden read leaves the terminal as it found it: with the context ended before it starts
// (nothing changed), ended while it waits (put back), and after a line typed.
func TestSecretReadRestoresTheTerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	defer ptmx.Close()
	defer tty.Close()
	fd := int(tty.Fd())
	before, _ := term.GetState(fd)
	same := func() bool { now, _ := term.GetState(fd); return reflect.DeepEqual(now, before) }

	for range 50 { // already ended: no window where the mode is changed after the restore
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := readSecret(ctx, fd); !errors.Is(err, context.Canceled) {
			t.Fatalf("a context already ended: %v", err)
		}
		if !same() {
			t.Fatal("a read cancelled before it began changed the terminal")
		}
	}
	go func() { time.Sleep(200 * time.Millisecond); ptmx.Write([]byte("s3cr\x7fret\r")) }() // once the read set the mode
	b, err := readSecret(context.Background(), fd)
	if err != nil || string(b) != "s3cret" || !same() {
		t.Fatalf("typed: %q %v, restored %v", b, err, same())
	}
	go func() { time.Sleep(200 * time.Millisecond); ptmx.Write([]byte("ab\x03")) }()
	if _, err := readSecret(context.Background(), fd); !errors.Is(err, errInterrupted) || !same() {
		t.Fatalf("a typed Ctrl-C: %v, restored %v", err, same())
	}
	// Last: the reader a cancel leaves behind would take what is typed next (the CLI exits then).
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	if _, err := readSecret(ctx, fd); !errors.Is(err, context.Canceled) || !same() {
		t.Fatalf("cancelled while waiting: %v, restored %v", err, same())
	}
	ptmx.Write([]byte("\n")) // the reader left behind ends: the tty can be closed
	time.Sleep(100 * time.Millisecond)
}

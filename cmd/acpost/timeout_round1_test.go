package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A local node that takes the connection and never answers (stopped, a handler stuck) is said to
// be stuck within the probe's time: exit 6, not a hang, and no second node started on its store.
func TestStuckNodeIsSaidNotWaitedFor(t *testing.T) {
	e := newEnv(t, inbox)
	os.MkdirAll(filepath.Dir(e.sock), 0o700)
	ln, err := net.Listen("unix", e.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c) // read nothing, answer nothing
		}
	}()
	start := time.Now()
	r := e.runFor(20*time.Second, "", "status")
	took := time.Since(start)
	if r.code != exitNoNode || !strings.Contains(r.err, "does not answer") {
		t.Fatalf("exit %d after %s: %s", r.code, took, r.err)
	}
	if took > probeTimeout+5*time.Second {
		t.Fatalf("took %s", took)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.sock), "acpost.pid")); err == nil {
		t.Fatal("a second node was started beside the stuck one")
	}
}

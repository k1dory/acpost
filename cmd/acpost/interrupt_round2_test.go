package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Ctrl-C while the local node's answer is half read (the body began, then nothing) ends the
// command: exit 130, and no node is started beside the one holding the socket. The cancelled read
// says "unavailable" and "canceled" both; it is an interrupt, not a missing node.
func TestInterruptWhileProbingStartsNothing(t *testing.T) {
	e := newEnv(t, inbox)
	os.MkdirAll(filepath.Dir(e.sock), 0o700)
	ln, err := net.Listen("unix", e.sock)
	if err != nil {
		t.Fatal(err)
	}
	hang := make(chan struct{})
	defer close(hang)
	var asked atomic.Bool
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":`))
		w.(http.Flusher).Flush()
		asked.Store(true)
		<-hang
	})}
	go srv.Serve(ln)
	defer srv.Close()
	var since time.Time
	ready := func() bool { // the partial body is out; give the client a moment to be reading it
		if !asked.Load() {
			return false
		}
		if since.IsZero() {
			since = time.Now()
		}
		return time.Since(since) > 200*time.Millisecond
	}
	code, stderr := startInterrupted(t, e, nil, ready, "status")
	if code != exitInterrupted {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	dir := filepath.Dir(e.sock)
	for _, f := range []string{"acpost.pid", "acpost.log"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("a node was started after the interrupt (%s is there)", f)
		}
	}
}

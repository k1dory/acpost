package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// Ctrl-C is exit 130 whatever the command made of its ended context: term ends cleanly on it
// (its shell closed, the terminal put back), and a node's error body cut short by it reads as the
// node's error. Neither is a success or the node's failure.
func TestInterruptIsAlwaysExit130(t *testing.T) {
	t.Run("term", func(t *testing.T) {
		e := newEnv(t, "agents:\n"+fakeAgent("worker", ", cwd: "+t.TempDir()))
		e.serve()
		ptmx, tty, err := pty.Open()
		if err != nil {
			t.Skip(err)
		}
		defer ptmx.Close()
		defer tty.Close()
		go func() { // the shell's output is read, so it never blocks on a full pty
			b := make([]byte, 4096)
			for {
				if _, err := ptmx.Read(b); err != nil {
					return
				}
			}
		}()
		before, err := term.GetState(int(tty.Fd()))
		if err != nil {
			t.Skip(err)
		}
		raw := func() bool { // term is attached: the terminal is raw
			now, err := term.GetState(int(tty.Fd()))
			return err == nil && !reflect.DeepEqual(now, before)
		}
		var since time.Time
		ready := func() bool {
			if !raw() {
				return false
			}
			if since.IsZero() {
				since = time.Now()
			}
			return time.Since(since) > 300*time.Millisecond
		}
		code, stderr := startInterrupted(t, e, tty, ready, "term", "--agent", "worker")
		if code != exitInterrupted {
			t.Fatalf("term: exit %d: %s", code, stderr)
		}
	})
	t.Run("a cut error body", func(t *testing.T) {
		e := newEnv(t, inbox)
		hang := make(chan struct{})
		var asked atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			w.Write([]byte(`{"error":`))
			w.(http.Flusher).Flush()
			asked.Store(true)
			<-hang
		}))
		defer srv.Close()
		defer close(hang)
		var since time.Time
		ready := func() bool {
			if !asked.Load() {
				return false
			}
			if since.IsZero() {
				since = time.Now()
			}
			return time.Since(since) > 200*time.Millisecond
		}
		code, stderr := startInterrupted(t, e, nil, ready, "agents", "--node", srv.URL)
		if code != exitInterrupted || !strings.Contains(stderr, "interrupted") {
			t.Fatalf("agents: exit %d: %s", code, stderr)
		}
	})
}

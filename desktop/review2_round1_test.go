//go:build darwin

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
)

// A terminal stream replaced by a second TermAttach emits nothing more: a chunk the old
// stream had read would interleave with the new stream's output (terminal output has no seq
// to drop it by).
func TestReplacedTermStreamEmitsNothingStale(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/terminals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(client.Terminal{ID: "t1", Running: true})
	})
	mux.HandleFunc("DELETE /v1/terminals/t1", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /v1/terminals/t1/output", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprintf(w, "id: 1\nevent: data\ndata: %s\n\n", base64.StdEncoding.EncodeToString([]byte("a")))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	n, _ := fakeNode(t, mux)
	var mu sync.Mutex
	var log []string
	in, release := make(chan struct{}), make(chan struct{})
	var gated atomic.Bool
	n.emitFn = func(name string, data any) {
		if gated.CompareAndSwap(false, true) {
			close(in)
			<-release // the first stream's chunk is held on its way
			mu.Lock()
			log = append(log, "stale")
			mu.Unlock()
			return
		}
		mu.Lock()
		log = append(log, name)
		mu.Unlock()
	}
	if _, err := n.OpenTerminal("", 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := n.TermAttach("t1", 0); err != nil {
		t.Fatal(err)
	}
	<-in
	again := make(chan struct{})
	go func() {
		n.TermAttach("t1", 1)
		mu.Lock()
		log = append(log, "attached again")
		mu.Unlock()
		close(again)
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-again
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if i, j := slices.Index(log, "stale"), slices.Index(log, "attached again"); i > j {
		t.Fatalf("the replaced stream's chunk came after the new attach: %v", log)
	}
}

// A start that timed out and stops slowly does not count as a running node: Status keeps
// trying, and once it has stopped a new node starts ("Try again" used to do nothing for good).
func TestSlowStoppingStartIsRetried(t *testing.T) {
	var starts atomic.Int32
	stopped := make(chan struct{})
	ownNode(t, func(ctx context.Context, ready func()) error {
		if starts.Add(1) == 1 {
			<-ctx.Done() // never ready; slow to stop once cancelled
			time.Sleep(300 * time.Millisecond)
			close(stopped)
			return ctx.Err()
		}
		ready()
		<-ctx.Done()
		return nil
	})
	oldStart, oldStop := nodeStartWait, nodeStopWait
	nodeStartWait, nodeStopWait = 100*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { nodeStartWait, nodeStopWait = oldStart, oldStop })
	n := &Node{emitFn: func(string, any) {}}
	t.Cleanup(func() { n.ServiceShutdown() })
	if err := n.ensureNode(); err == nil {
		t.Fatal("a start that never got ready: no error")
	}
	if c := n.Status(); c.Error == "" || starts.Load() != 1 {
		t.Fatalf("while the last start stops: %+v, %d starts", c, starts.Load())
	}
	<-stopped
	time.Sleep(20 * time.Millisecond)
	n.Status()
	if starts.Load() != 2 {
		t.Fatalf("%d starts after the last one stopped, want 2", starts.Load())
	}
}

// A saved file is made with the user's umask (077: 0600), as os.WriteFile made it; a file
// saved over keeps its mode.
func TestSaveKeepsUmaskAndMode(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.txt")
	cmd := exec.Command("/bin/sh", "-c", `umask 077; exec "$0" -test.run='^TestSaveHelper$'`, os.Args[0])
	cmd.Env = append(os.Environ(), "ACPOST_TEST_SAVE="+fresh)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if st, err := os.Stat(fresh); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("new file under umask 077: %v %v", st.Mode(), err)
	}
	kept := filepath.Join(dir, "kept.txt")
	os.WriteFile(kept, []byte("old"), 0o640)
	os.Chmod(kept, 0o640)
	if err := saveFile(kept, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(kept); st.Mode().Perm() != 0o640 {
		t.Fatalf("saved over: %v, want 0640", st.Mode())
	}
}

func TestSaveHelper(t *testing.T) {
	p := os.Getenv("ACPOST_TEST_SAVE")
	if p == "" {
		t.Skip("run by TestSaveKeepsUmaskAndMode")
	}
	if err := saveFile(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
}

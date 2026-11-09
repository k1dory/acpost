//go:build darwin

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// A batch of a stream that was replaced (Unfollow and Follow again) while the batch was on its
// way never reaches the window after the new stream started: the window would apply it to the
// new turn and then drop the replay of the events before it.
func TestReplacedFollowEmitsNothingStale(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/messages/m1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		for seq := 1; seq <= 100; seq++ {
			b, _ := json.Marshal(store.Event{Seq: int64(seq), Kind: "text"})
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, b)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	n, _ := fakeNode(t, mux)
	var mu sync.Mutex
	var log []string
	in, release := make(chan struct{}), make(chan struct{})
	var gated atomic.Bool
	n.emitFn = func(name string, data any) {
		if _, ok := data.(EventBatch); ok && gated.CompareAndSwap(false, true) {
			close(in)
			<-release // the first stream's first batch is held on its way
			mu.Lock()
			log = append(log, "stale")
			mu.Unlock()
			return
		}
		mu.Lock()
		log = append(log, name)
		mu.Unlock()
	}
	if err := n.Follow("m1", 0); err != nil {
		t.Fatal(err)
	}
	<-in
	refollowed := make(chan struct{})
	go func() {
		n.Unfollow("m1")
		n.Follow("m1", 0)
		mu.Lock()
		log = append(log, "followed again")
		mu.Unlock()
		close(refollowed)
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-refollowed
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if i, j := slices.Index(log, "stale"), slices.Index(log, "followed again"); i > j {
		t.Fatalf("the replaced stream's batch came after the new stream started: %v", log)
	}
}

// A terminal still opening when the app quits is closed on the node once its id arrives (on a
// node of another machine its shell went on), and no terminal opens after the app quit.
func TestTerminalOpeningAtShutdownIsClosed(t *testing.T) {
	var posts, closes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/terminals", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		time.Sleep(300 * time.Millisecond)
		json.NewEncoder(w).Encode(client.Terminal{ID: "t1", Running: true})
	})
	mux.HandleFunc("DELETE /v1/terminals/t1", func(w http.ResponseWriter, r *http.Request) { closes.Add(1); w.WriteHeader(204) })
	n, _ := fakeNode(t, mux)
	opened := make(chan error, 1)
	go func() { _, err := n.OpenTerminal("", 80, 24); opened <- err }()
	eventually(t, 2*time.Second, "the open is in flight", func() bool { return posts.Load() == 1 })
	n.ServiceShutdown()
	if closes.Load() != 1 {
		t.Fatalf("%d closes when shutdown returned, want 1", closes.Load())
	}
	if err := <-opened; err == nil {
		t.Fatal("a terminal opened as the app quit: no error")
	}
	if _, err := n.OpenTerminal("", 80, 24); err == nil || posts.Load() != 1 {
		t.Fatalf("opened after shutdown: %v, %d posts", err, posts.Load())
	}
}

// A 503 on the terminal's stream is a connection problem, not the shell's end: the stream comes
// back, the terminal stays the app's, and quitting closes it.
func TestTerminalStreamRetriesServerErrors(t *testing.T) {
	var reads, closes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/terminals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(client.Terminal{ID: "t1", Running: true})
	})
	mux.HandleFunc("DELETE /v1/terminals/t1", func(w http.ResponseWriter, r *http.Request) { closes.Add(1); w.WriteHeader(204) })
	mux.HandleFunc("GET /v1/terminals/t1/output", func(w http.ResponseWriter, r *http.Request) {
		if reads.Add(1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"code":"unavailable","message":"the node is busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprintf(w, "id: 5\nevent: data\ndata: %s\n\n", base64.StdEncoding.EncodeToString([]byte("hello")))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	n, rec := fakeNode(t, mux)
	if _, err := n.OpenTerminal("", 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := n.TermAttach("t1", 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "the output after the 503", func() bool {
		for _, e := range rec.all() {
			if e.data.(map[string]any)["data"] != nil {
				return true
			}
		}
		return false
	})
	for _, e := range rec.all() {
		if e.data.(map[string]any)["exit"] == true {
			t.Fatalf("a 503 taken as the shell's end: %v", e.data)
		}
	}
	n.ServiceShutdown()
	if closes.Load() != 1 {
		t.Fatalf("%d closes on shutdown, want 1", closes.Load())
	}
}

// A node that fails at once says why (the store's error, say), not that it "stopped".
func TestNodeFailureSaysWhy(t *testing.T) {
	ownNode(t, func(ctx context.Context, ready func()) error { return errors.New("database is locked") })
	for i := range 1000 {
		n := &Node{emitFn: func(string, any) {}}
		if err := n.ensureNode(); err == nil || !strings.Contains(err.Error(), "database is locked") {
			t.Fatalf("try %d: %v", i, err)
		}
	}
}

// "~/" and "~/src/" list what is in them, as "/…/" does: the trailing slash is kept.
func TestSearchDirsHomeWithSlash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{"src/a", "src/b"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	n := &Node{}
	if got := n.SearchDirs("~/src/"); !slices.Equal(got, []string{filepath.Join(home, "src/a"), filepath.Join(home, "src/b")}) {
		t.Fatalf("~/src/: %v", got)
	}
	if got := n.SearchDirs("~/"); !slices.Equal(got, []string{filepath.Join(home, "src")}) {
		t.Fatalf("~/: %v", got)
	}
}

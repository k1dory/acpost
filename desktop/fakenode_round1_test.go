//go:build darwin

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// A fake node over HTTP: /v1/health answers, the rest is the test's mux. The app's Node talks
// to it through ACPOST_NODE, as it would to a node of another machine.
func fakeNode(t *testing.T, mux *http.ServeMux) (*Node, *recorder) {
	t.Helper()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "node": "test", "limits": map[string]any{"max_body": 1 << 20, "max_files": 4, "max_files_size": 1 << 20}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("ACPOST_NODE", srv.URL)
	t.Setenv("ACPOST_TOKEN", "")
	t.Setenv("ACPOST_CA", "")
	rec := &recorder{}
	n := &Node{emitFn: rec.emit}
	if err := n.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.ServiceShutdown() })
	return n, rec
}

// recorder keeps what the Node emitted, in the order it was emitted.
type recorder struct {
	mu   sync.Mutex
	evs  []emitted
	slow func(data any) // runs inside emit: widens the window between taking a batch and emitting it
}

type emitted struct {
	name string
	data any
}

func (r *recorder) emit(name string, data any) {
	if r.slow != nil {
		r.slow(data)
	}
	r.mu.Lock()
	r.evs = append(r.evs, emitted{name, data})
	r.mu.Unlock()
}

func (r *recorder) all() []emitted {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]emitted(nil), r.evs...)
}

// eventually waits up to d for ok.
func eventually(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

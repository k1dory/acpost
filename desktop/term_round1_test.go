//go:build darwin

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
)

// termNode is a node with one terminal, t1: its output is "hello" and, after the connection
// breaks once, " world" and the shell's end. It records where each read started and the closes.
func termNode(t *testing.T) (*Node, *recorder, func() []string, *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	var froms []string
	var closes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/terminals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(client.Terminal{ID: "t1", Running: true})
	})
	mux.HandleFunc("DELETE /v1/terminals/t1", func(w http.ResponseWriter, r *http.Request) { closes.Add(1); w.WriteHeader(204) })
	mux.HandleFunc("GET /v1/terminals/t1/output", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		froms = append(froms, r.URL.Query().Get("from"))
		first := len(froms) == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		data := func(off int, s string) {
			fmt.Fprintf(w, "id: %d\nevent: data\ndata: %s\n\n", off+len(s), base64.StdEncoding.EncodeToString([]byte(s)))
			w.(http.Flusher).Flush()
		}
		if first {
			data(0, "hello")
			return // the connection breaks; the shell goes on
		}
		data(5, " world")
		code := 0
		b, _ := json.Marshal(client.Terminal{ID: "t1", ExitCode: &code})
		fmt.Fprintf(w, "event: end\ndata: %s\n\n", b)
	})
	n, rec := fakeNode(t, mux)
	return n, rec, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), froms...) }, &closes
}

// Nothing is emitted before the window attaches (it subscribes to term:<id> only once it has
// the id: what came first was lost). A broken connection resumes where it stopped and is told
// apart from the shell's end; the terminal's entry goes when its shell has ended.
func TestTerminalAttachResumes(t *testing.T) {
	n, rec, froms, _ := termNode(t)
	tm, err := n.OpenTerminal("", 80, 24)
	if err != nil || tm.ID != "t1" {
		t.Fatal(tm, err)
	}
	time.Sleep(200 * time.Millisecond)
	if evs := rec.all(); len(evs) != 0 {
		t.Fatalf("emitted before attaching: %v", evs)
	}
	if err := n.TermAttach("t1", 0); err != nil {
		t.Fatal(err)
	}
	var out string
	var exit, lost map[string]any
	eventually(t, 5*time.Second, "the shell's end", func() bool {
		out, exit, lost = "", nil, nil
		for _, e := range rec.all() {
			m := e.data.(map[string]any)
			if d, ok := m["data"].(string); ok {
				b, _ := base64.StdEncoding.DecodeString(d)
				out += string(b)
			}
			if m["lost"] == true {
				lost = m
			}
			if m["exit"] == true {
				exit = m
			}
		}
		return exit != nil
	})
	if out != "hello world" {
		t.Fatalf("output %q", out)
	}
	if lost == nil {
		t.Error("the broken connection was not told")
	}
	if exit["error"] != nil {
		t.Errorf("the shell's end said as an error: %v", exit)
	}
	if f := froms(); len(f) != 2 || f[0] != "0" || f[1] != "5" {
		t.Errorf("reads from %v, want [0 5]", f)
	}
	eventually(t, time.Second, "the ended terminal's entry goes", func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		return len(n.terms) == 0
	})
}

// The terminals the app opened are closed on the node when the app quits: a node of another
// machine kept their shells.
func TestShutdownClosesTerminals(t *testing.T) {
	n, _, _, closes := termNode(t)
	if _, err := n.OpenTerminal("", 80, 24); err != nil {
		t.Fatal(err)
	}
	n.ServiceShutdown()
	if closes.Load() != 1 {
		t.Fatalf("%d closes", closes.Load())
	}
}

//go:build darwin

package main

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
)

// closingNode is a node with one terminal, t1, whose closes fail (503) until fails of them did.
func closingNode(t *testing.T, fails int32) (*Node, *atomic.Int32) {
	t.Helper()
	var closes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/terminals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(client.Terminal{ID: "t1", Running: true})
	})
	mux.HandleFunc("DELETE /v1/terminals/t1", func(w http.ResponseWriter, r *http.Request) {
		if closes.Add(1) <= fails {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"code": "unavailable", "message": "busy"})
			return
		}
		w.WriteHeader(204)
	})
	oldWait := closeRetry
	closeRetry = 10 * time.Millisecond
	t.Cleanup(func() { closeRetry = oldWait })
	n, _ := fakeNode(t, mux)
	if _, err := n.OpenTerminal("", 80, 24); err != nil {
		t.Fatal(err)
	}
	return n, &closes
}

func hasTerm(n *Node, id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.terms[id]
	return ok
}

// A close that fails leaves the terminal the app's, and it is tried again until the node closed
// it. The entry used to go before the node answered: the shell went on, no one's.
func TestFailedTerminalCloseIsRetried(t *testing.T) {
	n, closes := closingNode(t, 2)
	if err := n.CloseTerminal("t1"); err == nil {
		t.Fatal("a close the node refused: no error")
	}
	if !hasTerm(n, "t1") {
		t.Fatal("the terminal was forgotten before the node closed it")
	}
	eventually(t, 2*time.Second, "the close is tried again until it is done", func() bool { return closes.Load() == 3 && !hasTerm(n, "t1") })
}

// A terminal whose close keeps failing is still closed by shutdown.
func TestShutdownClosesATerminalWhoseCloseFailed(t *testing.T) {
	n, closes := closingNode(t, 1<<30)
	closeTries = 0 // no retries: only shutdown is left to close it
	t.Cleanup(func() { closeTries = 8 })
	n.CloseTerminal("t1")
	before := closes.Load()
	n.ServiceShutdown()
	if closes.Load() != before+1 {
		t.Fatalf("shutdown sent %d closes, want 1", closes.Load()-before)
	}
}

// The node has no such terminal: it is ended, and no longer the app's.
func TestCloseOfAGoneTerminalForgetsIt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/terminals", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(client.Terminal{ID: "t1", Running: true})
	})
	mux.HandleFunc("DELETE /v1/terminals/t1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": "no terminal t1"})
	})
	n, _ := fakeNode(t, mux)
	if _, err := n.OpenTerminal("", 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := n.CloseTerminal("t1"); err != nil {
		t.Fatal(err)
	}
	if hasTerm(n, "t1") {
		t.Fatal("a terminal the node does not have is still the app's")
	}
}

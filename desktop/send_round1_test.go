//go:build darwin

package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// A send carries the composer's key: sent again after a lost answer, the node returns the
// first message instead of running the task twice.
func TestSendTaskIsIdempotent(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(store.Message{ID: "m1"})
	})
	n, _ := fakeNode(t, mux)
	for range 2 {
		if _, err := n.SendTask(Task{To: "agent:claude", Body: "hi", Idem: "op-1"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 2 || keys[0] != "op-1" || keys[1] != "op-1" {
		t.Fatalf("keys sent: %q", keys)
	}
}

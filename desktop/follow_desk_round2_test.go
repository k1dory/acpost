//go:build darwin

package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// Chat A, another chat, A again: the window's Unfollow of the first subscription may reach the
// app after the Follow of the second (each binding call runs in its own goroutine). It stops
// only its own subscription: the new stream goes on, and its batches say which they are of.
func TestLateUnfollowLeavesTheNewFollow(t *testing.T) {
	var opened, closed atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/messages/m1/events", func(w http.ResponseWriter, r *http.Request) {
		opened.Add(1)
		defer closed.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for i := 1; ; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
				fmt.Fprintf(w, "id: %d\nevent: event\ndata: {\"seq\":%d,\"kind\":\"text\",\"data\":\"\\\"x\\\"\"}\n\n", i, i)
				w.(http.Flusher).Flush()
			}
		}
	})
	n, rec := fakeNode(t, mux)
	if err := n.Follow("m1", 0, "s1"); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Second, "the first stream opens", func() bool { return opened.Load() == 1 })
	if err := n.Follow("m1", 0, "s2"); err != nil { // the chat opened again
		t.Fatal(err)
	}
	n.Unfollow("m1", "s1") // the first one's, late
	eventually(t, 2*time.Second, "the first stream closes", func() bool { return closed.Load() == 1 })
	time.Sleep(300 * time.Millisecond)
	if closed.Load() != 1 {
		t.Fatal("the late Unfollow of s1 stopped the stream of s2")
	}
	n.mu.Lock()
	f := n.follows["m1"]
	n.mu.Unlock()
	if f == nil || f.sub != "s2" {
		t.Fatalf("registered %+v, want s2", f)
	}
	var s2 bool
	for _, e := range rec.all() {
		if b, ok := e.data.(EventBatch); ok && len(b.Events) > 0 && b.Sub == "s2" {
			s2 = true
		}
	}
	if !s2 {
		t.Error("no batch of s2 came, or it did not say it is s2's")
	}
	n.Unfollow("m1", "s2")
	eventually(t, 2*time.Second, "Unfollow of s2 stops it", func() bool { return closed.Load() == 2 })
}

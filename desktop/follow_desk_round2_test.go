//go:build darwin

package main

import (
	"context"
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
	if err := n.Follow("m1", 0, 1); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Second, "the first stream opens", func() bool { return opened.Load() == 1 })
	if err := n.Follow("m1", 0, 2); err != nil { // the chat opened again
		t.Fatal(err)
	}
	n.Unfollow("m1", 1) // the first one's, late
	eventually(t, 2*time.Second, "the first stream closes", func() bool { return closed.Load() == 1 })
	time.Sleep(300 * time.Millisecond)
	if closed.Load() != 1 {
		t.Fatal("the late Unfollow of s1 stopped the stream of s2")
	}
	n.mu.Lock()
	f := n.follows["m1"]
	n.mu.Unlock()
	if f == nil || f.gen != 2 {
		t.Fatalf("registered %+v, want generation 2", f)
	}
	var s2 bool
	for _, e := range rec.all() {
		if b, ok := e.data.(EventBatch); ok && len(b.Events) > 0 && b.Gen == 2 {
			s2 = true
		}
	}
	if !s2 {
		t.Error("no batch of s2 came, or it did not say it is of generation 2")
	}
	n.Unfollow("m1", 2)
	eventually(t, 2*time.Second, "Unfollow of s2 stops it", func() bool { return closed.Load() == 2 })
}

// Chat A, another chat, A again, with the window's three calls (Follow 1, Unfollow 1, Follow 2)
// reaching the app in every order: it ends with the stream of generation 2, the only one the
// window wants. A late Follow 1 used to cancel stream 2 and take its place, and the Unfollow 1
// that followed left no stream at all.
func TestFollowCallsInAnyOrderEndWithTheNewest(t *testing.T) {
	calls := map[string]func(n *Node){
		"follow1":   func(n *Node) { n.Follow("m1", 0, 1) },
		"unfollow1": func(n *Node) { n.Unfollow("m1", 1) },
		"follow2":   func(n *Node) { n.Follow("m1", 0, 2) },
	}
	orders := [][]string{
		{"follow1", "unfollow1", "follow2"}, {"follow1", "follow2", "unfollow1"}, {"unfollow1", "follow1", "follow2"},
		{"unfollow1", "follow2", "follow1"}, {"follow2", "follow1", "unfollow1"}, {"follow2", "unfollow1", "follow1"},
	}
	for _, order := range orders {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			var open atomic.Int32
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1/messages/m1/events", func(w http.ResponseWriter, r *http.Request) {
				open.Add(1)
				defer open.Add(-1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			})
			n, _ := fakeNode(t, mux)
			for _, c := range order {
				calls[c](n)
			}
			n.mu.Lock()
			f := n.follows["m1"]
			n.mu.Unlock()
			if f == nil || f.gen != 2 {
				t.Fatalf("registered %+v, want generation 2", f)
			}
			eventually(t, 2*time.Second, "one stream open", func() bool { return open.Load() == 1 })
			time.Sleep(50 * time.Millisecond)
			if open.Load() != 1 {
				t.Fatalf("%d streams open, want 1", open.Load())
			}
		})
	}
}

// The same three calls at once, many times: whatever order the goroutines take, generation 2
// is the one registered.
func TestFollowCallsAtOnceEndWithTheNewest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/messages/m1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	n, _ := fakeNode(t, mux)
	for round := int64(0); round < 50; round++ {
		g1, g2 := 10*round+1, 10*round+2
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{}, 3)
		for _, f := range []func(){func() { n.Follow("m1", 0, g1) }, func() { n.Unfollow("m1", g1) }, func() { n.Follow("m1", 0, g2) }} {
			go func() { <-ctx.Done(); f(); done <- struct{}{} }()
		}
		cancel()
		for range 3 {
			<-done
		}
		n.mu.Lock()
		f := n.follows["m1"]
		n.mu.Unlock()
		if f == nil || f.gen != g2 {
			t.Fatalf("round %d: registered %+v, want generation %d", round, f, g2)
		}
	}
}

//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// Following a message again (chat A, then B, then A) keeps the new stream: Unfollow still
// reaches it, and its connection closes. The old stream's end used to delete the new
// registration, leaving an SSE connection nobody could stop.
func TestFollowAgainKeepsTheNewStream(t *testing.T) {
	var opened, closed atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/messages/m1/events", func(w http.ResponseWriter, r *http.Request) {
		opened.Add(1)
		defer closed.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				fmt.Fprint(w, ": alive\n\n")
				w.(http.Flusher).Flush()
			}
		}
	})
	n, _ := fakeNode(t, mux)
	for round := 0; round < 5; round++ {
		base := opened.Load()
		if err := n.Follow("m1", 0, ""); err != nil {
			t.Fatal(err)
		}
		eventually(t, 2*time.Second, "the first stream opens", func() bool { return opened.Load() == base+1 })
		n.Unfollow("m1", "")
		if err := n.Follow("m1", 0, ""); err != nil {
			t.Fatal(err)
		}
		eventually(t, 2*time.Second, "the second stream opens, the first closes", func() bool {
			return opened.Load() == base+2 && closed.Load() == base+1
		})
		n.Unfollow("m1", "")
		eventually(t, 2*time.Second, fmt.Sprintf("round %d: Unfollow closes the second stream", round), func() bool { return closed.Load() == base+2 })
	}
}

// The batches of a stream reach the window in order, and the final after the last of them:
// the window drops a batch whose seq is not after the last it applied.
func TestFollowBatchesInOrder(t *testing.T) {
	const total = 10000
	taken := make(chan struct{}, 1) // a batch before the last event is on its way to the window
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/messages/m1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		send := func(seq int) {
			b, _ := json.Marshal(store.Event{Seq: int64(seq), Kind: "text"})
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, b)
		}
		for seq := 1; seq < total; seq++ {
			send(seq)
			if seq%500 == 0 {
				w.(http.Flusher).Flush()
			}
		}
		w.(http.Flusher).Flush()
		// The last event and the end come while the ticker's batch is being emitted.
		for len(taken) > 0 {
			<-taken
		}
		select {
		case <-taken:
		case <-time.After(time.Second):
		}
		send(total)
		b, _ := json.Marshal(store.Message{ID: "m1", State: store.Done})
		fmt.Fprintf(w, "event: end\ndata: %s\n\n", b)
		w.(http.Flusher).Flush()
	})
	n, rec := fakeNode(t, mux)
	rec.slow = func(data any) {
		if b := data.(EventBatch); b.Final == nil && len(b.Events) > 0 && b.Events[len(b.Events)-1].Seq < total {
			select {
			case taken <- struct{}{}:
			default:
			}
			time.Sleep(80 * time.Millisecond)
		}
	}
	for round := 0; round < 3; round++ {
		rec.mu.Lock()
		rec.evs = nil
		rec.mu.Unlock()
		if err := n.Follow("m1", 0, ""); err != nil {
			t.Fatal(err)
		}
		eventually(t, 20*time.Second, "the final", func() bool {
			for _, e := range rec.all() {
				if b := e.data.(EventBatch); b.Final != nil {
					return true
				}
			}
			return false
		})
		time.Sleep(200 * time.Millisecond) // anything emitted late is caught
		var last int64
		got, final := 0, false
		for _, e := range rec.all() {
			b := e.data.(EventBatch)
			if final {
				t.Fatalf("round %d: a batch after the final (%d events, first seq %d)", round, len(b.Events), b.Events[0].Seq)
			}
			for _, ev := range b.Events {
				if ev.Seq <= last {
					t.Fatalf("round %d: seq %d after %d", round, ev.Seq, last)
				}
				last = ev.Seq
				got++
			}
			final = b.Final != nil
		}
		if got != total || !final {
			t.Fatalf("round %d: %d of %d events, final %v", round, got, total, final)
		}
	}
}

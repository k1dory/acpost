package node_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// lateSend holds the first send a node makes to the hub: the node's request fails (its connection
// is closed), and the send reaches the hub only when released, long after.
type lateSend struct {
	mu      sync.Mutex
	held    bool
	release chan struct{}
	done    chan struct{}
}

func (l *lateSend) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		first := r.Method == "POST" && r.URL.Path == "/v1/messages" && !l.held
		l.held = l.held || first
		l.mu.Unlock()
		if !first {
			h.ServeHTTP(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		late := r.Clone(context.Background())
		late.Body = io.NopCloser(bytes.NewReader(body))
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
		go func() {
			defer close(l.done)
			<-l.release
			h.ServeHTTP(httptest.NewRecorder(), late)
		}()
	})
}

// A send held on its way to the hub past the time its absence counts: the node does not call it
// never sent while it can still arrive. It fences it on the hub, and the late send, a repeat of
// the fence there, does not run: the hub's agent never gets the message.
func TestLateSendAfterAbsenceDoesNotRun(t *testing.T) {
	node.FastHubForTest(t, 30*time.Millisecond) // the hub's absence counts after 300 ms
	l := &lateSend{release: make(chan struct{}), done: make(chan struct{})}
	hub, edge, _ := hubPairWith(t, l.wrap)
	hub.MaintainEveryForTest(50 * time.Millisecond)
	ctx := context.Background()
	user := edge.LocalUser()
	if _, _, err := edge.Send(ctx, user, node.SendRequest{To: "agent:bot@hub", Body: "held on the way", IdemKey: "late"}); err == nil {
		t.Fatal("the held send succeeded")
	}
	var id string
	deadline := time.Now().Add(10 * time.Second)
	for id == "" || !edgeSettled(edge, id) {
		if time.Now().After(deadline) {
			t.Fatal("the copy was never settled")
		}
		if id == "" {
			if list, _, err := edge.List(ctx, user, store.Filter{Limit: 10}); err == nil && len(list) == 1 {
				id = list[0].ID
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(l.release) // the send arrives now
	<-l.done
	if m, ok, err := hub.Claim(ctx, hub.LocalUser(), "bot", 500*time.Millisecond, time.Minute); err != nil || ok {
		t.Fatalf("the hub's bot got %s (%v): a send its sender was told about ran", m.ID, err)
	}
	got, err := edge.Wait(ctx, user, id, 10*time.Second)
	if err != nil || !got.State.Final() || got.State == store.Done {
		t.Fatalf("the sender's copy: %s %v", got.State, err)
	}
	if hm, err := hub.Get(ctx, hub.LocalUser(), id); err != nil || hm.State != got.State {
		t.Fatalf("the hub has %s (%v), the sender %s", hm.State, err, got.State)
	}
}

// edgeSettled says the node no longer waits for the hub's word on the copy (not forwarding).
func edgeSettled(edge *node.Node, id string) bool {
	m, err := edge.Get(context.Background(), edge.LocalUser(), id)
	return err == nil && m.Origin != "forwarding"
}

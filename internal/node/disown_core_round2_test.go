package node_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// lostSend holds the first send a node makes to the hub: the node gets the start of an answer
// (headers promising more than comes) and then a closed connection, so its transport cannot send
// the request again; the request reaches the hub only when released, long after.
type lostSend struct {
	mu      sync.Mutex
	held    bool
	release chan struct{}
	late    chan int // the status the hub gave the late send
}

func (l *lostSend) wrap(h http.Handler) http.Handler {
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
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"msg_`))
		w.(http.Flusher).Flush()
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
		go func() {
			<-l.release
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, late)
			l.late <- rec.Code
		}()
	})
}

// A send held on its way past the time its absence counts: the node has the hub disown it, the
// copy ends as not sent, and the send arriving after that is refused, not run. Its key is free: a
// send again with it goes through.
func TestLateSendAfterDisownRefused(t *testing.T) {
	node.FastHubForTest(t, 30*time.Millisecond) // absence is asked about after 300 ms
	l := &lostSend{release: make(chan struct{}), late: make(chan int, 1)}
	hub, edge, edgeStore := hubPairWith(t, l.wrap)
	ctx := context.Background()
	user := edge.LocalUser()
	if _, _, err := edge.Send(ctx, user, node.SendRequest{To: "agent:bot@hub", Body: "held on the way", IdemKey: "late"}); err == nil {
		t.Fatal("the held send succeeded")
	}
	m, err := edgeStore.ByIdem(ctx, user.Address(), "late")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := edgeStore.Get(ctx, m.ID)
		if err == nil && got.Origin == "unsent" && got.State == store.Failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy is %s %q", got.State, got.Origin)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(l.release)
	if code := <-l.late; code != http.StatusForbidden {
		t.Fatalf("the late send got %d, want refused (403)", code)
	}
	if got, ok, err := hub.Claim(ctx, hub.LocalUser(), "bot", 300*time.Millisecond, time.Minute); err != nil || ok {
		t.Fatalf("the hub's bot got %s (%v): a send its sender gave up ran", got.ID, err)
	}
	again, _, err := edge.Send(ctx, user, node.SendRequest{To: "agent:bot@hub", Body: "again", IdemKey: "late"})
	if err != nil || again.ID == m.ID {
		t.Fatalf("a send again with the key: %s %v", again.ID, err)
	}
	if got, ok, err := hub.Claim(ctx, hub.LocalUser(), "bot", 5*time.Second, time.Minute); err != nil || !ok || got.ID != again.ID {
		t.Fatalf("the send again did not reach the hub's bot: %v %v", ok, err)
	}
}

// The hub's disown: only the sending node's, and a message it has is answered, not refused.
func TestHubDisown(t *testing.T) {
	hub, edge, _ := hubPair(t)
	ctx := context.Background()
	peer := node.Principal{Kind: "node", Name: "edge"}
	sent, _, err := hub.Send(ctx, peer, node.SendRequest{To: "agent:bot", Body: "b", ID: store.NewID(), From: "user:u@edge"})
	if err != nil {
		t.Fatal(err)
	}
	if m, found, err := hub.Disown(ctx, peer, sent.ID, "user:u@edge", ""); err != nil || !found || m.ID != sent.ID {
		t.Fatalf("a message the hub has: %v %v", found, err)
	}
	if _, _, err := hub.Disown(ctx, node.Principal{Kind: "node", Name: "other"}, sent.ID, "user:u@edge", ""); err == nil {
		t.Fatal("another node disowned edge's send")
	}
	if _, _, err := hub.Disown(ctx, edge.LocalUser(), sent.ID, "user:u@edge", ""); err == nil {
		t.Fatal("a user disowned a node's send")
	}
	id := store.NewID()
	if _, found, err := hub.Disown(ctx, peer, id, "user:u@edge", ""); err != nil || found {
		t.Fatalf("a message the hub has not: %v %v", found, err)
	}
	if _, _, err := hub.Send(ctx, peer, node.SendRequest{To: "agent:bot", Body: "late", ID: id, From: "user:u@edge"}); err == nil {
		t.Fatal("a disowned send was taken")
	}
}

// A hub before disown: the route is missing there and its catch-all answers 404 not_found. Past
// the wait the copy ends as not sent, as on main, its key free: not forwarding for ever.
func TestOldHubWithoutDisownEndsLostSend(t *testing.T) {
	node.FastHubForTest(t, 30*time.Millisecond)
	l := &lostSend{release: make(chan struct{}), late: make(chan int, 1)}
	old := func(h http.Handler) http.Handler {
		h = l.wrap(h)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/disown") {
				r.URL.Path += "-unknown" // what this hub's mux does not have: its catch-all answers
			}
			h.ServeHTTP(w, r)
		})
	}
	_, edge, edgeStore := hubPairWith(t, old)
	ctx := context.Background()
	user := edge.LocalUser()
	if _, _, err := edge.Send(ctx, user, node.SendRequest{To: "agent:bot@hub", Body: "lost", IdemKey: "old"}); err == nil {
		t.Fatal("the lost send succeeded")
	}
	m, err := edgeStore.ByIdem(ctx, user.Address(), "old")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := edgeStore.Get(ctx, m.ID)
		if err == nil && got.Origin == "unsent" && got.State == store.Failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy is %s %q: blocked on a hub without disown", got.State, got.Origin)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := edgeStore.ByIdem(ctx, user.Address(), "old"); err == nil {
		t.Fatal("the key stayed taken")
	}
	close(l.release)
	<-l.late
}

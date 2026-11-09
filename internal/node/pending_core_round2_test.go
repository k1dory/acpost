package node

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// With the hub away, sends the hub has not answered for stop at limits.max_queued_per_agent for
// their address (3 here), each new key refused as unavailable; a repeat of a key still answers.
func TestPendingForwardsBoundedByQueueLimit(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.down = true })
	n.hub = f.client(t)
	refused := 0
	for i := range 6 {
		_, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "w", IdemKey: "k" + string(rune('a'+i))})
		if err != nil && strings.Contains(err.Error(), "max_queued_per_agent") {
			refused++
		}
	}
	if refused != 3 {
		t.Errorf("%d of 6 sends refused for the limit, want 3", refused)
	}
	ids, err := n.st.OfOrigin(ctx, originForwarding, "", store.MaxPage)
	if err != nil || len(ids) != 3 {
		t.Fatalf("%d copies waiting for the hub (%v), want 3", len(ids), err)
	}
	// Another address has its own room.
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:near@other", Body: "w", IdemKey: "z"}); err != nil && strings.Contains(err.Error(), "max_queued_per_agent") {
		t.Fatalf("another address was refused: %v", err)
	}
	// A repeat of a waiting key is answered as before: not yet known, not refused for room.
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "w", IdemKey: "ka"}); err == nil || strings.Contains(err.Error(), "max_queued_per_agent") {
		t.Fatalf("a repeat: %v", err)
	}
}

// Many copies forwarding while the hub is slow to answer: the hub is asked about a bounded number
// of them at once, not about each on its own schedule.
func TestForwardResolversBounded(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	var now, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := now.Add(1)
		defer now.Add(-1)
		for p := peak.Load(); c > p && !peak.CompareAndSwap(p, c); p = peak.Load() {
		}
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	n := newNode(t, hubCfg)
	c, err := client.New(srv.URL, "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	n.hub = c
	for range 60 {
		if _, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originForwarding}); err != nil {
			t.Fatal(err)
		}
	}
	n.resumeForwards() // as a start finds them
	time.Sleep(1500 * time.Millisecond)
	if p := peak.Load(); p == 0 || p > 8 {
		t.Fatalf("the hub was asked about %d forwards at once (bound 8)", p)
	}
}

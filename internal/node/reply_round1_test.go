package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// finalDelivered stores a delivered message under origin, already done here.
func finalDelivered(t *testing.T, n *Node, origin string) store.Message {
	t.Helper()
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot", Body: "b", Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := n.st.Claim(ctx, "agent:bot", time.Minute)
	if err != nil || !ok || c.ID != m.ID {
		t.Fatal(ok, err)
	}
	if err := n.st.Complete(ctx, c.ID, c.LeaseID, store.Done, "the answer", "", ""); err != nil {
		t.Fatal(err)
	}
	return m
}

// A hub away for longer than a few tries gets the outcome when it is back: once, the lease kept
// meanwhile, and the outcome marked as taken.
func TestRelayHandsReplyWhenHubIsBack(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "L1")
	f.set(func(f *fakeHub) { f.down = true })
	n.hub = f.client(t)
	m := finalDelivered(t, n, originHub+"L1")
	done := make(chan struct{})
	go func() { n.relay(m.ID, "L1"); close(done) }()
	// (The old relay gave up after 21 tries, a second apart.)
	eventuallyWithin(t, 30*time.Second, "the reply is tried more than the old 21 times", func() bool { return f.count("reply") >= 25 })
	select {
	case <-done:
		t.Fatal("the relay gave the outcome up while the hub was away")
	default:
	}
	f.set(func(f *fakeHub) { f.down = false })
	<-done
	if rs := f.get().replies; len(rs) != 1 || rs[0].Body != "the answer" {
		t.Fatalf("hub got %+v", rs)
	}
	if f.count("lease") == 0 {
		t.Fatal("the lease was not renewed while the hub was away")
	}
	if got, _ := n.st.Get(ctx, m.ID); got.Origin != originAcked+"L1" {
		t.Fatalf("outcome not marked as taken: %q", got.Origin)
	}
}

// A node started with an outcome its hub did not confirm hands it; one confirmed is not handed again.
func TestStartHandsUnconfirmedReply(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "L1")
	n.hub = f.client(t)
	m := finalDelivered(t, n, originHub+"L1")
	finalDelivered(t, n, originAcked+"L0")
	n.startJoin()
	eventually(t, "the unconfirmed outcome is handed", func() bool { return len(f.get().replies) == 1 })
	time.Sleep(100 * time.Millisecond)
	if got := f.replyCalls("L0"); got != 0 {
		t.Fatalf("a confirmed outcome was handed again (%d)", got)
	}
	if got, _ := n.st.Get(ctx, m.ID); got.Origin != originAcked+"L1" {
		t.Fatalf("outcome not marked as taken: %q", got.Origin)
	}
}

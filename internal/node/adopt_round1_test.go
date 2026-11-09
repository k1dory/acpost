package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The hub's lease L1 is lost while the message runs here (the hub reaped it and delivers it again
// under L2): the copy is not cancelled, the redelivery adopts it, its outcome goes back under L2,
// and nothing failed is sent for L1.
func TestHubLeaseLostLeavesRunningCopyForRedelivery(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "L1")
	n.hub = f.client(t)
	d := store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot@edge", Body: "work", LeaseID: "L1"}
	n.deliver(d)
	c, ok, err := n.Claim(ctx, me, "bot", time.Second, time.Minute)
	if err != nil || !ok || c.ID != d.ID {
		t.Fatal(ok, err)
	}
	// The hub reaped L1 and holds the message for its next delivery.
	f.set(func(f *fakeHub) { f.lease, f.state = "", store.Queued })
	eventually(t, "the relay of L1 renews and learns the lease is gone", func() bool { return f.get().lease == "" && f.count("lease") >= 1 })
	time.Sleep(150 * time.Millisecond)
	if got, _ := n.st.Get(ctx, d.ID); got.State != store.Running {
		t.Fatalf("a lost hub lease ended the copy running here: %s", got.State)
	}
	// Delivered again under L2: adopted, the same turn goes on.
	f.set(func(f *fakeHub) { f.lease, f.state = "L2", store.Running })
	d2 := d
	d2.LeaseID = "L2"
	n.deliver(d2)
	if got, _ := n.st.Get(ctx, d.ID); got.Origin != originHub+"L2" || got.State != store.Running {
		t.Fatalf("redelivery not adopted: origin %q state %s", got.Origin, got.State)
	}
	if err := n.Reply(ctx, me, c.ID, c.LeaseID, "the answer", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the outcome goes back under L2", func() bool { return len(f.get().replies) > 0 })
	time.Sleep(100 * time.Millisecond)
	rs := f.get().replies
	if len(rs) != 1 || rs[0].Lease != "L2" || rs[0].Body != "the answer" || rs[0].Error != "" {
		t.Fatalf("hub got %+v", rs)
	}
}

// A full queue does not refuse a redelivery of a message already here: it is adopted, nothing
// failed goes back to the hub.
func TestRedeliveryAdoptedWhenQueueFull(t *testing.T) {
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "L1")
	n.hub = f.client(t)
	d := store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot@edge", Body: "work", LeaseID: "L1"}
	n.deliver(d)
	for range 2 {
		if _, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "more"}); err != nil {
			t.Fatal(err)
		}
	}
	f.set(func(f *fakeHub) { f.lease = "L2" })
	d.LeaseID = "L2"
	n.deliver(d)
	time.Sleep(100 * time.Millisecond)
	if rs := f.get().replies; len(rs) != 0 {
		t.Fatalf("a full queue refused a redelivery: %+v", rs)
	}
	if got, _ := n.st.Get(ctx, d.ID); got.Origin != originHub+"L2" || got.State != store.Queued {
		t.Fatalf("not adopted: %q %s", got.Origin, got.State)
	}
	// A new delivery still meets the limit.
	n.deliver(store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot@edge", Body: "new", LeaseID: "L2"})
	eventually(t, "a new delivery over the limit is refused", func() bool { return len(f.get().replies) == 1 })
}

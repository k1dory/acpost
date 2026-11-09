package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A relay of an old delivery cannot end a copy a new delivery adopted, whatever the hub says.
func TestOldRelayCannotCancelAdoptedCopy(t *testing.T) {
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "L2")
	n.hub = f.client(t)
	d := store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot@edge", Body: "work", LeaseID: "L1"}
	if _, _, err := n.st.Insert(ctx, &store.Message{ID: d.ID, From: d.From, To: "agent:bot", Body: d.Body, Origin: originHub + "L1"}); err != nil {
		t.Fatal(err)
	}
	d.LeaseID = "L2"
	if why, ok := n.takeDelivery(ctx, &store.Message{ID: d.ID, From: d.From, To: "agent:bot", Body: d.Body, Origin: originHub + "L2"}, 0, 3); !ok {
		t.Fatal(why)
	}
	f.set(func(f *fakeHub) { f.state = store.Cancelled })
	n.hubLeaseGone(d.ID, "L1") // what the relay of L1 does on its conflict
	if got, _ := n.st.Get(ctx, d.ID); got.State != store.Queued || got.Origin != originHub+"L2" {
		t.Fatalf("the old relay changed the adopted copy: %s %q", got.State, got.Origin)
	}
	if ok, err := n.st.CancelIfOrigin(ctx, d.ID, originHub+"L1", "", ""); ok || err != nil {
		t.Fatal("cancelled under the old origin", ok, err)
	}
	// The hub cancelling the message for real ends it, under the lease that holds it.
	n.hubLeaseGone(d.ID, "L2")
	if got, _ := n.st.Get(ctx, d.ID); got.State != store.Cancelled {
		t.Fatalf("a hub cancel did not reach the copy: %s", got.State)
	}
}

// A copy this node ended because the hub gave the message up runs again when the hub delivers it
// again; its attempts are kept.
func TestRedeliveryRequeuesCopyTheHubGaveUp(t *testing.T) {
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "L1")
	n.hub = f.client(t)
	d := store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot@edge", Body: "work", LeaseID: "L1"}
	n.deliver(d)
	if _, ok, _ := n.Claim(ctx, me, "bot", time.Second, time.Minute); !ok {
		t.Fatal("no claim")
	}
	f.set(func(f *fakeHub) { f.state, f.lease, f.errText = store.Failed, "", "lease lost after 3 attempts" })
	n.hubLeaseGone(d.ID, "L1")
	got, _ := n.st.Get(ctx, d.ID)
	if got.State != store.Cancelled || got.ErrorCode != store.CodeHubGaveUp {
		t.Fatalf("copy of a message the hub gave up: %s %q", got.State, got.ErrorCode)
	}
	f.set(func(f *fakeHub) { f.state, f.lease = store.Running, "L2" })
	d.LeaseID = "L2"
	n.deliver(d)
	got, _ = n.st.Get(ctx, d.ID)
	if got.State != store.Queued || got.Attempts != 1 || got.Origin != originHub+"L2" || got.ErrorCode != "" {
		t.Fatalf("not run again: %s attempts %d origin %q code %q", got.State, got.Attempts, got.Origin, got.ErrorCode)
	}
}

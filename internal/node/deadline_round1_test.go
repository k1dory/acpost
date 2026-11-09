package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// shortHubCalls bounds each call to the hub at d for this test.
func shortHubCalls(t *testing.T, d time.Duration) {
	old, oldWait := hubCallTimeout, hubClaimWait
	hubCallTimeout, hubClaimWait = d, d
	t.Cleanup(func() { hubCallTimeout, hubClaimWait = old, oldWait })
}

// A hub that accepts the announce and never answers does not stop the node announcing: the call
// ends at its deadline and the loop goes on.
func TestAnnounceLoopOutlivesHungHub(t *testing.T) {
	shortHubCalls(t, 100*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.hang = true })
	n.hub = f.client(t)
	n.startJoin()
	eventually(t, "the announce is made again after a hung one", func() bool { return f.count("announce") >= 2 })
	eventually(t, "the claim is made again after a hung one", func() bool { return f.count("claim") >= 2 })
}

// A relay whose lease renewal hangs renews it again later: the hub does not lose the lease to one
// hung call.
func TestRelayOutlivesHungExtend(t *testing.T) {
	shortHubCalls(t, 100*time.Millisecond)
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "L1")
	n.hub = f.client(t)
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot", Body: "b", Origin: originHub + "L1"})
	if err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) { f.hang = true })
	n.goLoop(func() { n.relay(m.ID, "L1") })
	eventually(t, "a renewal is tried", func() bool { return f.count("lease") >= 1 })
	f.set(func(f *fakeHub) { f.hang = false })
	eventually(t, "renewals go on after a hung one", func() bool { return f.count("lease") >= 4 })
}

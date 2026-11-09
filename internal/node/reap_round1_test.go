package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// After the process stood still past a lease, the maintenance loop does not requeue a turn this
// process still runs; a lease nobody here holds is reaped as before.
func TestReapSkipsTurnsRunningHere(t *testing.T) {
	n := newNode(t, inboxCfg)
	for range 2 {
		if _, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "work"}); err != nil {
			t.Fatal(err)
		}
	}
	live, ok, err := n.st.Claim(ctx, "agent:bot", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	dead, ok, err := n.st.Claim(ctx, "agent:bot", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	tl := n.watchTurn(live, time.Minute)
	defer tl.stop()
	// Both leases ran out while the process stood still.
	for _, m := range []store.Message{live, dead} {
		if err := n.st.Extend(ctx, m.ID, m.LeaseID, -time.Second); err != nil {
			t.Fatal(err)
		}
	}
	n.setMaintainEvery(20 * time.Millisecond)
	eventually(t, "the dead lease is reaped", func() bool {
		m, _ := n.st.Get(ctx, dead.ID)
		return m.State == store.Queued
	})
	got, _ := n.st.Get(ctx, live.ID)
	if got.State != store.Running || got.LeaseID != live.LeaseID {
		t.Fatalf("a turn running here was reaped: %s", got.State)
	}
	if tl.lost.Load() {
		t.Fatal("the turn's watch lost its lease")
	}
}

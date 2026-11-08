package node

import (
	"context"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The client gives up right after the hub took the message: the local copy is there all the
// same, and follows the hub's.
func TestForwardKeepsCopyWhenRequestEnds(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state = store.Queued })
	n.hub = f.client(t)
	rctx, cancel := context.WithCancel(ctx)
	f.set(func(f *fakeHub) { f.onSend = cancel })
	_, _, _ = n.Send(rctx, me, SendRequest{To: "agent:far@other", Body: "work"})
	sent := f.get().sent
	if len(sent) != 1 {
		t.Fatal("not sent to the hub")
	}
	id := sent[0].ID
	eventually(t, "the local copy follows the hub's", func() bool {
		m, err := n.st.Get(ctx, id)
		return err == nil && m.Origin == originRemote
	})
}

// A repeat with the same key while the hub is away returns the message sent first, from here.
func TestForwardRepeatNeedsNoHub(t *testing.T) {
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state = store.Queued })
	n.hub = f.client(t)
	first, created, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "k1"})
	if err != nil || !created {
		t.Fatal(created, err)
	}
	f.set(func(f *fakeHub) { f.down = true })
	again, created, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "k1"})
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("repeat: %v created=%v id %s, first %s", err, created, again.ID, first.ID)
	}
}

// A send the hub did not answer is stored, and ends as not sent once the hub says it never got it.
func TestForwardUnansweredEndsWhenHubHasNone(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.down = true })
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "k2"}); code(err) != "unavailable" {
		t.Fatalf("hub away: %v", err)
	}
	m, err := n.st.ByIdem(ctx, me.Address(), "k2")
	if err != nil || m.Origin != originForwarding {
		t.Fatalf("not stored before sending: %+v %v", m, err)
	}
	f.set(func(f *fakeHub) { f.down, f.missing = false, true })
	eventually(t, "the copy ends as not sent", func() bool {
		got, _ := n.st.Get(ctx, m.ID)
		return got.State == store.Failed
	})
}

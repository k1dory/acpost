package node

import (
	"context"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// heldSend makes the hub keep its answer to the next send until release is called; received is
// told when the hub has taken it.
func heldSend(f *fakeHub) (received chan struct{}, release func()) {
	received, hold := make(chan struct{}, 1), make(chan struct{})
	f.set(func(f *fakeHub) { f.hold, f.received = hold, received })
	return received, func() { f.set(func(f *fakeHub) { f.hold = nil }); close(hold) }
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: " + what)
	}
}

// The client gives up once the hub has the message and before its answer: the send fails for the
// client, the local copy is there all the same and follows the hub's.
func TestForwardKeepsCopyWhenRequestEnds(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state = store.Queued })
	n.hub = f.client(t)
	received, release := heldSend(f)
	rctx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		_, _, err := n.Send(rctx, me, SendRequest{To: "agent:far@other", Body: "work"})
		errc <- err
	}()
	waitFor(t, received, "the hub takes the send")
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("a send whose request ended said it was sent")
	}
	release()
	id := f.get().sent[0].ID
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

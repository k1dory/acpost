package node

// Regression tests for the third review of PR #7 (Codex).

import (
	"context"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The hub answers a send with its first send of the key, whose copy is here already (made by an
// earlier forward, without the key), and the send was cancelled here meanwhile: that copy takes the
// key, and the cancel reaches the hub through it.
func TestDedupOntoExistingCopyKeepsCancelAndKey(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: "t", State: store.Queued}
	f.set(func(f *fakeHub) {
		f.state, f.msgs[first.ID], f.keys["user:tester@edge|k7"] = store.Queued, first, first.ID
	})
	if _, _, err := n.st.Insert(ctx, &store.Message{ID: first.ID, From: me.Address(), To: first.To, Body: "work", Thread: "t", Origin: originRemote}); err != nil {
		t.Fatal(err)
	}
	n.hub = f.client(t)
	received, release := heldSend(f)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "k7"})
	}()
	waitFor(t, received, "the hub takes the send")
	m, err := n.st.ByIdem(ctx, me.Address(), "k7")
	if err != nil || m.ID == first.ID {
		t.Fatal(m.ID, err)
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	release()
	<-done
	eventually(t, "the cancel reaches the hub's first message", func() bool {
		return f.cancelled(first.ID)
	})
	eventually(t, "the copy of the first message is cancelled", func() bool {
		got, _ := n.st.Get(ctx, first.ID)
		return got.State == store.Cancelled
	})
	if got, err := n.st.ByIdem(ctx, me.Address(), "k7"); err != nil || got.ID != first.ID {
		t.Fatalf("the key is not on the first message: %+v %v", got.ID, err)
	}
}

// A cancel of a copy whose send's answer was lost, while the hub (out of reach) has already
// finished the message: Cancel, Wait and Follow report no outcome before the hub's verdict, and
// the one they report then is the hub's.
func TestCancelOfForwardWaitsForHubVerdict(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	n.hub = f.client(t)
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originForwarding})
	if err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) {
		f.state, f.reply, f.down = store.Done, "the hub's answer", true
		f.msgs[m.ID] = store.Message{ID: m.ID, To: "agent:far@other"}
	})
	followed := make(chan store.Message, 1)
	go func() {
		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		got, _ := n.Follow(fctx, me, m.ID, 0, func(store.Event) error { return nil })
		followed <- got
	}()
	got, err := n.Cancel(ctx, me, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State.Final() {
		t.Fatalf("Cancel reported %s before the hub's verdict", got.State)
	}
	if w, _ := n.Wait(ctx, me, m.ID, 300*time.Millisecond); w.State.Final() {
		t.Fatalf("Wait reported %s before the hub's verdict", w.State)
	}
	select {
	case fm := <-followed:
		t.Fatalf("Follow ended with %s before the hub's verdict", fm.State)
	default:
	}
	f.set(func(f *fakeHub) { f.down = false })
	w, err := n.Wait(ctx, me, m.ID, 5*time.Second)
	if err != nil || w.State != store.Done || w.Reply != "the hub's answer" {
		t.Fatalf("Wait: %s %q %v", w.State, w.Reply, err)
	}
	if fm := <-followed; fm.State != store.Done {
		t.Fatalf("Follow ended with %s", fm.State)
	}
}

// A copy whose cancel was asked for, found never to have reached the hub, ends cancelled (not
// failed), its key free.
func TestNotSentAfterCancelEndsCancelled(t *testing.T) {
	n := newNode(t, hubCfg)
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originForwarding, IdemKey: "c3"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := n.st.WantCancel(ctx, m.ID, originForwarding); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := n.st.FinishForward(ctx, m.ID, originForwarding, originRemote, store.Message{To: m.To, Thread: m.ID}); ok || err != nil {
		t.Fatalf("a send finished over the cancel asked for: %v %v", ok, err)
	}
	if !n.notSent(ctx, m.ID, context.Canceled) {
		t.Fatal("not settled")
	}
	got, _ := n.st.Get(ctx, m.ID)
	if got.State != store.Cancelled || got.Origin != originUnsent {
		t.Fatalf("%s %s", got.State, got.Origin)
	}
	if _, err := n.st.ByIdem(ctx, me.Address(), "c3"); err == nil {
		t.Fatal("the key stayed")
	}
}

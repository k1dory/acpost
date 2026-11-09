package node

// Regression tests for the second review of PR #7 that need what this round added (store seams,
// the store's one-step replacement).

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A refused send whose "not sent" write fails is still settled: a resolver takes it, and a send
// again with the key is sent.
func TestNotSentWriteFailureResolved(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	old := forwardNotSent
	var failed atomic.Bool
	forwardNotSent = func(s *store.Store, c context.Context, id, a, b, why string) (bool, error) {
		if failed.CompareAndSwap(false, true) {
			return false, errors.New("disk I/O error")
		}
		return old(s, c, id, a, b, why)
	}
	t.Cleanup(func() { forwardNotSent = old })
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state, f.refuse, f.missing = store.Queued, 1, true })
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "n1"}); code(err) != "refused" {
		t.Fatalf("refused: %v", err)
	}
	eventually(t, "a send again with the key is sent", func() bool {
		m, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "n1"})
		return err == nil && m.Origin == originRemote
	})
}

// The one-step replacement changes nothing when it fails half-way (the copy does not fit).
func TestReplaceForwardAtomic(t *testing.T) {
	st, err := store.Open("memory:", store.Options{MaxStore: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, _, err := st.Insert(ctx, &store.Message{ID: store.NewID(), From: "user:a", To: "agent:x@n", Body: "w", Origin: "forwarding", IdemKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	first := store.Message{ID: store.NewID(), To: "agent:x@n", Body: strings.Repeat("x", 6<<20)}
	if _, err := st.ReplaceForward(ctx, b.ID, "forwarding", "remote", first); !errors.Is(err, store.ErrStoreFull) {
		t.Fatalf("replacement over the store's limit: %v", err)
	}
	if got, err := st.ByIdem(ctx, "user:a", "k"); err != nil || got.ID != b.ID || got.Origin != "forwarding" {
		t.Fatalf("the copy or its key went: %+v %v", got, err)
	}
	if _, err := st.Get(ctx, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("half a replacement stored")
	}
	first.Body = "work"
	got, err := st.ReplaceForward(ctx, b.ID, "forwarding", "remote", first)
	if err != nil || got.Origin != "remote" {
		t.Fatalf("%+v %v", got, err)
	}
	if again, err := st.ByIdem(ctx, "user:a", "k"); err != nil || again.ID != first.ID {
		t.Fatalf("the key did not move: %+v %v", again, err)
	}
}

// A send finishing between Cancel's read of the copy and its cancel: the cancel goes to the hub.
func TestCancelRacingFinishGoesToHub(t *testing.T) {
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	n.hub = f.client(t)
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originForwarding})
	if err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) { f.state, f.msgs[m.ID] = store.Queued, store.Message{ID: m.ID, To: "agent:far@other"} })
	old := afterCancelRead
	afterCancelRead = func(id string) {
		_, _ = n.st.FinishForward(ctx, id, originForwarding, originRemote, store.Message{To: "agent:far@other", Thread: id})
	}
	t.Cleanup(func() { afterCancelRead = old })
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.get().cancels; len(got) != 1 || got[0] != m.ID {
		t.Fatalf("the hub got no cancel: %v", got)
	}
}

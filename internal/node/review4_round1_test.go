package node

// Regression tests for the fourth review of PR #7 (Codex).

import (
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A hub of the version before the lookup by key holds the first send of a key; a repeat's answer
// is lost and the repeat is cancelled. The hub's 404 for the lookup proves nothing: the copy is
// not taken for unsent, and its key stays.
func TestOldHubLookupIsNoProofOfAbsence(t *testing.T) {
	fastHub(t, 30*time.Millisecond) // absence would count after 300 ms
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: "t", State: store.Running}
	f.set(func(f *fakeHub) {
		f.state, f.missing, f.loseAnswer, f.oldRoutes = store.Running, true, 1, true
		f.msgs[first.ID], f.keys["user:tester@edge|o1"] = first, first.ID
	})
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "o1"}); err == nil {
		t.Fatal("the lost answer did not fail the send")
	}
	m, err := n.st.ByIdem(ctx, me.Address(), "o1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	got, err := n.st.ByIdem(ctx, me.Address(), "o1")
	if err != nil || got.ID != m.ID {
		t.Fatalf("the key was released: %v", err)
	}
	if got.State.Final() || got.Origin != originForwarding {
		t.Fatalf("taken for unsent on an old hub's 404: %s %s", got.State, got.Origin)
	}
}

// The hub kept the first send of a key with files over this node's limits; the repeat is
// cancelled while its answer is out. The refusal of the files does not hold the cancel back: the
// hub gets it for its message, and the copy settles with the key, without the files.
func TestRefusedFilesDoNotBlockDedupCancel(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, strings.Replace(hubCfg, "limits: {", "limits: {max_files_size: 4MiB, ", 1))
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: "t", State: store.Queued,
		Files: store.Files{{Name: "big.bin", Size: 8 << 20, SHA256: strings.Repeat("0", 64)}}}
	f.set(func(f *fakeHub) {
		f.state, f.msgs[first.ID], f.keys["user:tester@edge|b1"] = store.Queued, first, first.ID
	})
	n.hub = f.client(t)
	received, release := heldSend(f)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "b1"})
	}()
	waitFor(t, received, "the hub takes the send")
	m, err := n.st.ByIdem(ctx, me.Address(), "b1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	release()
	<-done
	eventually(t, "the cancel reaches the hub's first message", func() bool {
		c := f.get().cancels
		return len(c) >= 1 && c[len(c)-1] == first.ID
	})
	eventually(t, "the copy of the first message is cancelled", func() bool {
		got, _ := n.st.Get(ctx, first.ID)
		return got.State == store.Cancelled
	})
	if got, err := n.st.ByIdem(ctx, me.Address(), "b1"); err != nil || got.ID != first.ID || len(got.Files) != 0 {
		t.Fatalf("the key is not on the first message's copy: %+v %v", got.ID, err)
	}
}

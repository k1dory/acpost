package node

// Regression tests for the fifth review of PR #7 (Codex): no failure of the dedup replacement
// holds back a cancel, and the replacement has the room of the copy it drops.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The store's limit holds either copy's file, not both: the replacement takes the dropped copy's
// room first.
func TestReplaceForwardTakesRoomOfDroppedCopy(t *testing.T) {
	st, err := store.Open("memory:", store.Options{MaxStore: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, _, err := st.Insert(ctx, &store.Message{ID: store.NewID(), From: "user:a", To: "agent:x@n", Body: "w", Origin: "forwarding", IdemKey: "k",
		Files: store.Files{{Name: "a.bin", Data: []byte(strings.Repeat("a", 3<<20))}}})
	if err != nil {
		t.Fatal(err)
	}
	first := store.Message{ID: store.NewID(), To: "agent:x@n", Body: "w", Files: store.Files{{Name: "b.bin", Data: []byte(strings.Repeat("b", 3<<20))}}}
	got, err := st.ReplaceForward(ctx, b.ID, "forwarding", "remote", first)
	if err != nil {
		t.Fatalf("replacement within the limit: %v", err)
	}
	if again, err := st.ByIdem(ctx, "user:a", "k"); err != nil || again.ID != got.ID || len(again.Files) != 1 {
		t.Fatalf("%+v %v", again.ID, err)
	}
}

// A send cancelled while its answer is out, the hub answering with its first send of the key,
// whose copy cannot be made here in every way it can fail: the hub gets the cancel for its message
// in each, and the cancelled copy and its key stay until a copy can be made.
func TestDedupCancelSurvivesEveryReplacementFailure(t *testing.T) {
	type row struct {
		name  string
		cfg   string
		first func(*store.Message)
		setup func(t *testing.T, f *fakeHub)
	}
	big := func(n int) string { return strings.Repeat("x", n) }
	for _, r := range []row{
		{name: "store full", cfg: "max_store: 4MiB, ", first: func(m *store.Message) { m.Body = big(6 << 20) }},
		{name: "files refused", cfg: "max_files_size: 4MiB, ", first: func(m *store.Message) {
			m.Files = store.Files{{Name: "big.bin", Size: 8 << 20, SHA256: strings.Repeat("0", 64)}}
		}},
		{name: "download fails", first: func(m *store.Message) {
			m.Files = store.Files{{Name: "f.bin", Size: 1, SHA256: strings.Repeat("0", 64)}}
		}, setup: func(t *testing.T, f *fakeHub) {
			old := hubFilesTimeout
			hubFilesTimeout = 50 * time.Millisecond
			t.Cleanup(func() { hubFilesTimeout = old })
			f.hangFiles = 1 << 20
		}},
		{name: "write error", setup: func(t *testing.T, f *fakeHub) {
			old := replaceForward
			replaceForward = func(*store.Store, context.Context, string, string, string, store.Message) (store.Message, error) {
				return store.Message{}, errors.New("disk I/O error")
			}
			t.Cleanup(func() { replaceForward = old })
		}},
	} {
		t.Run(r.name, func(t *testing.T) {
			fastHub(t, 30*time.Millisecond)
			n := newNode(t, strings.Replace(hubCfg, "limits: {", "limits: {"+r.cfg, 1))
			f := newFakeHub(t, "")
			first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: "t", State: store.Queued}
			if r.first != nil {
				r.first(&first)
			}
			f.set(func(f *fakeHub) {
				f.state, f.missing, f.msgs[first.ID], f.keys["user:tester@edge|z1"] = store.Queued, true, first, first.ID
				if r.setup != nil {
					r.setup(t, f)
				}
			})
			n.hub = f.client(t)
			received, release := heldSend(f)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _, _ = n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "z1"})
			}()
			waitFor(t, received, "the hub takes the send")
			m, err := n.st.ByIdem(ctx, me.Address(), "z1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := n.Cancel(ctx, me, m.ID); err != nil {
				t.Fatal(err)
			}
			release()
			<-done
			eventually(t, "the hub gets the cancel for its message", func() bool {
				for _, c := range f.get().cancels {
					if c == first.ID {
						return true
					}
				}
				return false
			})
			if got, err := n.st.ByIdem(ctx, me.Address(), "z1"); err != nil || (got.ID != m.ID && got.ID != first.ID) {
				t.Fatalf("the key went: %v", err)
			}
		})
	}
}

// TestFailedReplacementKeepsCopy with a cancel: the answer is lost, the resolver finds the first
// send by its key, its copy does not fit here; the cancel reaches the hub all the same.
func TestFailedReplacementCancelReachesHub(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, strings.Replace(hubCfg, "limits: {", "limits: {max_store: 4MiB, ", 1))
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: strings.Repeat("x", 6<<20), Thread: "t", State: store.Queued}
	f.set(func(f *fakeHub) {
		f.state, f.missing, f.loseAnswer = store.Queued, true, 1
		f.msgs[first.ID], f.keys["user:tester@edge|r2"] = first, first.ID
	})
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "r2"}); err == nil {
		t.Fatal("the lost answer did not fail the send")
	}
	m, err := n.st.ByIdem(ctx, me.Address(), "r2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the hub gets the cancel for its message", func() bool {
		c := f.get().cancels
		return len(c) > 0 && c[0] == first.ID
	})
	if got, err := n.st.ByIdem(ctx, me.Address(), "r2"); err != nil || got.ID != m.ID {
		t.Fatalf("the copy and its key went: %v", err)
	}
}

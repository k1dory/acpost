package node

// Regression tests for the first review of the node and hub round (Codex, PR #7).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// A send the hub refused is not sent: started again (the same backlog item, the same key) once
// the hub takes messages, it is sent, once.
func TestRefusedForwardSentOnRetry(t *testing.T) {
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state, f.refuse = store.Queued, 1 })
	n.hub = f.client(t)
	b, err := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "Review it", Agent: "agent:far@other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.StartBacklogItem(ctx, me, b.ID, ""); code(err) != "refused" {
		t.Fatalf("the hub refused: %v", err)
	}
	m, err := n.StartBacklogItem(ctx, me, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.State.Final() || m.Origin != originRemote || len(f.get().msgs) != 1 {
		t.Fatalf("the retry was answered with the refusal: %s %q, %d on the hub", m.State, m.Origin, len(f.get().msgs))
	}
	if _, err := n.st.BacklogItemOf(ctx, b.ID); !errors.Is(err, store.ErrNoBacklogItem) {
		t.Fatal("the item stayed after it was sent")
	}
}

// The local copy's time to live does not end a message the hub took while its answer was late:
// the copy becomes the hub's, and its outcome is taken.
func TestForwardTTLDoesNotEndCopyTheHubTook(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state = store.Running })
	n.hub = f.client(t)
	n.setMaintainEvery(20 * time.Millisecond)
	received, release := heldSend(f)
	type res struct {
		m   store.Message
		err error
	}
	done := make(chan res, 1)
	go func() {
		m, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", TTL: 100 * time.Millisecond})
		done <- res{m, err}
	}()
	waitFor(t, received, "the hub takes the send")
	time.Sleep(300 * time.Millisecond) // past the TTL, maintenance running
	release()
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	f.set(func(f *fakeHub) { f.state = store.Done })
	eventually(t, "the hub's outcome is taken", func() bool {
		got, _ := n.st.Get(ctx, r.m.ID)
		return got.State == store.Done
	})
}

// A copy cancelled here while its send was on the way: the cancel reaches the hub once it has the
// message; and after a restart, a cancel the last process did not carry there is carried.
func TestCancelDuringForwardReachesHub(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state = store.Queued })
	n.hub = f.client(t)
	received, release := heldSend(f)
	go func() { _, _, _ = n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "c1"}) }()
	waitFor(t, received, "the hub takes the send")
	m, created, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "c1"})
	if err != nil || created {
		t.Fatal(created, err)
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	release()
	eventually(t, "the hub gets the cancel", func() bool { return len(f.get().cancels) == 1 && f.get().cancels[0] == m.ID })
	eventually(t, "the copy is the hub's", func() bool {
		got, _ := n.st.Get(ctx, m.ID)
		return got.Origin == originRemote && got.State == store.Cancelled
	})

	// The last process cancelled one and stopped before the hub heard.
	left, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originForwarding})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.st.Cancel(ctx, left.ID); err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) { f.msgs[left.ID] = store.Message{ID: left.ID, State: store.Queued} })
	n.resumeForwards()
	eventually(t, "the left cancel reaches the hub", func() bool { return len(f.get().cancels) == 2 })
}

// The hub kept a send of a key under the id of its first send, and the answer was lost: the copy
// becomes the copy of that first message, not a false "not sent".
func TestLostAnswerOfDeduplicatedSend(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: "t", State: store.Queued}
	f.set(func(f *fakeHub) {
		f.state, f.missing, f.loseAnswer = store.Queued, true, 1
		f.msgs[first.ID], f.keys["user:tester@edge|d1"] = first, first.ID
	})
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "d1"}); err == nil {
		t.Fatal("the lost answer did not fail the send")
	}
	second := f.get().sent[0].ID
	eventually(t, "the copy follows the hub's first message", func() bool {
		got, err := n.st.Get(ctx, first.ID)
		return err == nil && got.Origin == originRemote
	})
	if _, err := n.st.Get(ctx, second); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the copy of the repeat stayed: %v", err)
	}
	if m, err := n.st.ByIdem(ctx, me.Address(), "d1"); err != nil || m.ID != first.ID {
		t.Fatalf("the key does not lead to the hub's message: %v %v", m.ID, err)
	}
}

// The hub cancelled a delivered message and its first answer to "what became of it" failed: the
// node asks again, and the copy is cancelled (no redelivery comes for a cancelled message).
func TestHubCancelReachesCopyAfterFailedCheck(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state, f.getFails = store.Cancelled, 2 })
	n.hub = f.client(t)
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot", Body: "b", Origin: originHub + "L1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := n.st.Claim(ctx, "agent:bot", time.Minute); !ok {
		t.Fatal("no claim")
	}
	n.goLoop(func() { n.relay(m.ID, "L1") })
	eventually(t, "the copy is cancelled", func() bool {
		got, _ := n.st.Get(ctx, m.ID)
		return got.State == store.Cancelled
	})
}

// Two cloud operations whose keys share the first 24 bits of their hash each make their own
// sandbox.
func TestCloudOperationsWithCollidingShortIDs(t *testing.T) {
	f := &namedSandboxd{}
	n := cloudNode(t, f, "")
	seen := map[string]string{}
	var k1, k2 string
	for i := 0; k2 == ""; i++ {
		k := fmt.Sprintf("op-%d", i)
		sum := sha256.Sum256([]byte(n.login + "\x00" + k))
		short := hex.EncodeToString(sum[:3])
		if prev, ok := seen[short]; ok {
			k1, k2 = prev, k
		}
		seen[short] = k
	}
	a, err := n.CreateCloudTask(ctx, me, client.CloudTask{Agent: "assistant", Name: "Fix it", Idem: k1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := n.CreateCloudTask(ctx, me, client.CloudTask{Agent: "assistant", Name: "Fix it", Idem: k2, Repo: "https://git.example/o/other"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Sandbox == b.Sandbox || f.posts != 2 {
		t.Fatalf("%s and %s took one sandbox (%d made)", k1, k2, f.posts)
	}
}

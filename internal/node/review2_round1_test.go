package node

// Regression tests for the second review of the node and hub round (Codex, PR #7).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A backlog start whose send got no answer, started again before the hub's answer is learned:
// the item stays (the retry is told to wait), and once the hub is known not to have it, a start
// sends it.
func TestRetryBeforeAbsenceVerdictKeepsBacklogItem(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state, f.down = store.Queued, true })
	n.hub = f.client(t)
	b, err := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "Review it", Agent: "agent:far@other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.StartBacklogItem(ctx, me, b.ID, ""); code(err) != "unavailable" {
		t.Fatalf("hub away: %v", err)
	}
	f.set(func(f *fakeHub) { f.down, f.missing = false, true })
	if m, err := n.StartBacklogItem(ctx, me, b.ID, ""); code(err) != "unavailable" {
		t.Fatalf("a retry before the hub's answer is known: %+v %v", m, err)
	}
	if _, err := n.st.BacklogItemOf(ctx, b.ID); err != nil {
		t.Fatalf("the item left the backlog unsent: %v", err)
	}
	eventually(t, "the send is known not to have reached the hub", func() bool {
		m, err := n.StartBacklogItem(ctx, me, b.ID, "")
		return err == nil && m.Origin == originRemote
	})
	if got := len(f.get().msgs); got != 1 {
		t.Fatalf("%d on the hub", got)
	}
}

// The hub kept a send under an earlier send's id, and the copy of that one cannot be stored (the
// store is full): the copy of this send and its key stay, nothing is lost.
func TestFailedReplacementKeepsCopy(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, strings.Replace(hubCfg, "limits: {", "limits: {max_store: 4MiB, ", 1))
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: strings.Repeat("x", 6<<20), Thread: "t", State: store.Queued}
	f.set(func(f *fakeHub) {
		f.state, f.missing, f.loseAnswer = store.Queued, true, 1
		f.msgs[first.ID], f.keys["user:tester@edge|r1"] = first, first.ID
	})
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "r1"}); err == nil {
		t.Fatal("the lost answer did not fail the send")
	}
	second := f.get().sent[0].ID
	time.Sleep(300 * time.Millisecond) // the resolver tries to replace it, and fails
	if _, err := n.st.Get(ctx, second); err != nil {
		t.Fatalf("the copy went with the failed replacement: %v", err)
	}
	if key, err := n.st.IdemKeyOf(ctx, second); err != nil || key != "r1" {
		t.Fatalf("the key went with the failed replacement: %q %v", key, err)
	}
}

// A copy cancelled here while forwarding, of a message the hub had already finished: the hub
// refuses the cancel, and the copy takes the hub's outcome.
func TestCancelConflictTakesHubOutcome(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	n.hub = f.client(t)
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originForwarding})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.st.Cancel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) {
		f.state, f.reply = store.Done, "the hub's answer"
		f.msgs[m.ID] = store.Message{ID: m.ID, To: "agent:far@other"}
	})
	n.resumeForwards()
	eventually(t, "the copy takes the hub's outcome", func() bool {
		got, _ := n.st.Get(ctx, m.ID)
		return got.State == store.Done && got.Reply == "the hub's answer"
	})
}

// A send that reaches the hub late (after its request ended, while the hub looked empty) is not
// taken for unsent: the copy follows the hub's, and a cancel done here meanwhile reaches the hub.
func TestLateSendNotTakenForUnsent(t *testing.T) {
	fastHub(t, 30*time.Millisecond) // the hub's absence counts after 300 ms
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	holdIn := make(chan struct{})
	f.set(func(f *fakeHub) { f.state, f.missing, f.holdIn = store.Queued, true, holdIn })
	n.hub = f.client(t)
	rctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, _, err := n.Send(rctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "l1"}); err == nil {
		t.Fatal("the request ended before the hub took it")
	}
	m, err := n.st.ByIdem(ctx, me.Address(), "l1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got, _ := n.st.Get(ctx, m.ID); got.Origin == originUnsent {
		t.Fatal("taken for unsent while the send may still arrive")
	}
	f.set(func(f *fakeHub) { f.holdIn = nil })
	close(holdIn) // the send arrives
	eventually(t, "the cancel reaches the hub", func() bool { c := f.get().cancels; return len(c) == 1 && c[0] == m.ID })
	if got, _ := n.st.Get(ctx, m.ID); got.Origin != originRemote {
		t.Fatalf("origin %q", got.Origin)
	}
}

// A send cancelled here while the hub answers with its first send of the key (another id): the
// cancel goes with the copy of that message, to the hub.
func TestDedupKeepsLocalCancel(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: "t", State: store.Queued}
	f.set(func(f *fakeHub) {
		f.state, f.msgs[first.ID], f.keys["user:tester@edge|k9"] = store.Queued, first, first.ID
	})
	n.hub = f.client(t)
	received, release := heldSend(f)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "k9"})
	}()
	waitFor(t, received, "the hub takes the send")
	m, err := n.st.ByIdem(ctx, me.Address(), "k9")
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
		return len(c) == 1 && c[0] == first.ID
	})
	eventually(t, "the copy of the first message takes the hub's cancel", func() bool {
		got, _ := n.st.Get(ctx, first.ID)
		return got.State == store.Cancelled
	})
}

// A lease lost while the hub holds the message for its next delivery, and the sender cancels it
// before any delivery: the copy here learns it and ends.
func TestQueuedThenCancelledWithoutRedelivery(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state = store.Queued })
	n.hub = f.client(t)
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot", Body: "b", Origin: originHub + "L1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := n.st.Claim(ctx, "agent:bot", time.Minute); !ok {
		t.Fatal("no claim")
	}
	n.goLoop(func() { n.relay(m.ID, "L1") })
	eventually(t, "the hub is asked", func() bool { return f.count(m.ID) >= 1 })
	time.Sleep(100 * time.Millisecond)
	f.set(func(f *fakeHub) { f.state = store.Cancelled })
	eventually(t, "the copy is cancelled", func() bool {
		got, _ := n.st.Get(ctx, m.ID)
		return got.State == store.Cancelled
	})
}

// A download of a kept message's files that hangs, while the resolver replaces a copy: it ends at
// its deadline, and the next try gets them.
func TestHungFilesDownloadRecovers(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	old := hubFilesTimeout
	hubFilesTimeout = 100 * time.Millisecond
	t.Cleanup(func() { hubFilesTimeout = old })
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	first := store.Message{ID: store.NewID(), From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: "t", State: store.Queued,
		Files: store.Files{{Name: "f.bin", Size: 1, SHA256: strings.Repeat("0", 64)}}}
	f.set(func(f *fakeHub) {
		f.state, f.missing, f.loseAnswer, f.hangFiles = store.Queued, true, 1, 1
		f.msgs[first.ID], f.keys["user:tester@edge|f1"] = first, first.ID
	})
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "f1"}); err == nil {
		t.Fatal("the lost answer did not fail the send")
	}
	eventually(t, "the copy of the kept message, with its files", func() bool {
		got, err := n.st.Get(ctx, first.ID)
		return err == nil && got.Origin == originRemote && len(got.Files) == 1
	})
	if c := f.get(); c.files < 2 {
		t.Fatalf("files asked %d times", c.files)
	}
}

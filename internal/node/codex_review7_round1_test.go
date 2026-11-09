package node

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// An attempt reads 100 tokens and ends (a timeout) but the node stops before its Complete; the
// message is requeued at the restart and the retry reads the same run to 150: nothing of the
// first attempt was published, so the event, this node's count and a receiving node's count are 150.
func TestUsageBeforeAStopIsPublishedFromTheRetry(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	n := sandboxNode(t, f)
	n.sbMu.Lock()
	a := n.sandboxes["cloud"]
	n.sbMu.Unlock()
	m := store.Message{ID: store.NewID(), From: me.Address(), To: "agent:ghost", Body: "count me"}
	if _, _, err := n.st.Insert(ctx, &m); err != nil {
		t.Fatal(err)
	}
	attempt := func(input int) store.Message {
		c, ok, err := n.st.Claim(ctx, "agent:ghost", time.Minute)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		tr := &sandboxTurn{a: a, msgID: m.ID, run: sandboxRun{ID: "r7", Session: "s-1"}}
		tr.used, tr.usage = true, store.Usage{Input: input}
		a.recordUsage(m.ID, c.LeaseID, tr) // what the attempt read, on its way to its outcome
		return c
	}
	attempt(100)
	evs, _ := n.st.Events(ctx, m.ID, 0, 100)
	for _, e := range evs {
		if e.Kind == "usage" {
			t.Fatalf("published before the outcome: %s", e.Data)
		}
	}
	// The node stops here, before the Complete; at its start the message is queued again.
	if _, err := n.st.RequeueRunning(ctx, []string{"agent:ghost"}, 3); err != nil {
		t.Fatal(err)
	}
	c := attempt(150)
	if err := n.st.Complete(ctx, m.ID, c.LeaseID, store.Failed, "", "turn_timeout", "late"); err != nil {
		t.Fatal(err)
	}
	evs, _ = n.st.Events(ctx, m.ID, 0, 100)
	var data []string
	for _, e := range evs {
		if e.Kind == "usage" {
			data = append(data, e.Data)
		}
	}
	var u store.Usage
	if len(data) != 1 || json.Unmarshal([]byte(data[0]), &u) != nil || u.Input != 150 || inputUsage(t, n) != 150 {
		t.Fatalf("usage events %q, counted %d: want one event of 150 and 150 counted", data, inputUsage(t, n))
	}
	r, err := store.Open("memory:", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rm := store.Message{ID: m.ID, From: "user:a", To: "agent:cloud@n", Body: "hi"}
	if _, _, err := r.Insert(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	if err := r.AddEvent(ctx, m.ID, "usage", data[0]); err != nil {
		t.Fatal(err)
	}
	if sums, err := r.UsageOf(ctx, store.UsageQuery{}); err != nil || len(sums) != 1 || sums[0].Input != 150 {
		t.Fatalf("the receiving node counts %+v %v, want 150", sums, err)
	}
}

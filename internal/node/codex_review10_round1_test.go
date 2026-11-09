package node

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// Twenty usage lines of 100 tokens in the stream; the message is cancelled while the second is
// between its reading and its storing: the attempt stores the rest once at its end, so the count,
// the sum of the usage events (no more than 16) and a receiving store's count all agree.
func TestCancelMidStreamUsageAgrees(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{"USER:6"}
	for i := int64(0); i < 20; i++ {
		f.frames = append(f.frames, frame(7+i, "r1", "log", `{"type":"usage","model":"claude-x","input_tokens":100}`, nil))
	}
	reached, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32 // usage lines read (the hook is called for each, stored or kept)
	sandboxUsageRead = func() {
		if calls.Add(1) == 2 {
			close(reached)
			<-release
		}
	}
	handled := make(chan string, 4)
	sandboxHandled = func(id string) { handled <- id }
	t.Cleanup(func() { sandboxUsageRead, sandboxHandled = func() {}, func(string) {} })
	n, _ := sandboxNodeOn(t, f.handler, "store: \"memory:\"\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "build it"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the second usage line was never read")
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	usage := func() (sum, events int, data []string) {
		evs, _ := n.st.Events(ctx, m.ID, 0, 1000)
		for _, e := range evs {
			if e.Kind == "usage" {
				var u store.Usage
				_ = json.Unmarshal([]byte(e.Data), &u)
				sum += u.Input
				events++
				data = append(data, e.Data)
			}
		}
		return
	}
	// The attempt ends (its stream closed by the cancel) and stores what it read: every line read
	// is counted, in at most three events (the cancel's, the line it caught, the attempt's end).
	select {
	case id := <-handled:
		if id != m.ID {
			t.Fatalf("handled %s", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the attempt never ended")
	}
	sum, events, data := usage()
	counted := int(inputUsage(t, n))
	if read := int(calls.Load()) * 100; counted != read || sum != read {
		t.Fatalf("lines read %d (×100), counted %d, usage events %d summing to %d", calls.Load(), counted, events, sum)
	}
	if events > 3 {
		t.Fatalf("%d usage events for one cancelled attempt: want at most 3", events)
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
	for _, d := range data {
		if err := r.AddEvent(ctx, m.ID, "usage", d); err != nil {
			t.Fatal(err)
		}
	}
	if sums, err := r.UsageOf(ctx, store.UsageQuery{}); err != nil || len(sums) != 1 || int(sums[0].Input) != counted {
		t.Fatalf("the receiving node counts %+v %v, want %d", sums, err, counted)
	}
	t.Logf("counted %d in %d events", counted, events)
}

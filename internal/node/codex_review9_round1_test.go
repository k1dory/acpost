package node

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A sandbox attempt has stored 100 tokens and read 50 more when the message is cancelled, before
// it stores them: the reading is still taken (the attempt's lease ended, no other attempt runs
// it), and the count, the sum of the usage events and a receiving node's count are 150.
func TestSandboxReadingCaughtByACancelIsKept(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{
		"USER:6",
		frame(7, "r1", "log", `{"type":"usage","model":"claude-x","input_tokens":100}`, nil),
		frame(8, "r1", "log", `{"type":"usage","input_tokens":50}`, nil),
	}
	reached, release := make(chan struct{}), make(chan struct{})
	calls := 0
	sandboxUsageRead = func() {
		calls++
		if calls == 2 { // the second line: read, not stored yet
			close(reached)
			<-release
		}
	}
	t.Cleanup(func() { sandboxUsageRead = func() {} })
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
	var sum int
	var data []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		sum, data = 0, nil
		evs, _ := n.st.Events(ctx, m.ID, 0, 1000)
		for _, e := range evs {
			if e.Kind == "usage" {
				var u store.Usage
				_ = json.Unmarshal([]byte(e.Data), &u)
				sum += u.Input
				data = append(data, e.Data)
			}
		}
		if sum == 150 && inputUsage(t, n) == 150 {
			break
		}
	}
	if sum != 150 || inputUsage(t, n) != 150 {
		t.Fatalf("after the cancel: events sum %d (%q), counted %d; want 150 and 150", sum, data, inputUsage(t, n))
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
	if sums, err := r.UsageOf(ctx, store.UsageQuery{}); err != nil || len(sums) != 1 || sums[0].Input != 150 {
		t.Fatalf("the receiving node counts %+v %v, want 150", sums, err)
	}
}

// An adapter that offers neither session/load nor session/resume: a task's next message after its
// process stopped goes on in a new session (as it always did), the new id stored and a notice
// said; a message naming the old session by id fails with the reason.
func TestTaskGoesOnWhenTheAgentCannotReopen(t *testing.T) {
	n := roundNode(t, map[string]string{"FAKE_NO_LOAD": "1"}, "")
	if w := ask(t, n, SendRequest{Body: "count", Task: "T"}); w.State != store.Done || w.Reply != "1" {
		t.Fatalf("first: %s %q %q", w.State, w.Reply, w.Error)
	}
	first := storedSession(t, n, "task:T")
	n.acp["fake"].slots[0].stopProc() // idle_timeout: the process is gone
	w := ask(t, n, SendRequest{Body: "count", Task: "T"})
	if w.State != store.Done || w.Reply != "1" {
		t.Fatalf("after the stop: %s %q %s %q", w.State, w.Reply, w.ErrorCode, w.Error)
	}
	if again := storedSession(t, n, "task:T"); again == first {
		t.Fatalf("the task still maps to the session %s it cannot open", first)
	}
	evs, _ := n.st.Events(ctx, w.ID, 0, 100)
	said := false
	for _, e := range evs {
		said = said || (e.Kind == "notice" && strings.Contains(e.Data, first))
	}
	if !said {
		t.Fatal("no notice that the earlier context is lost")
	}
	if w := ask(t, n, SendRequest{Body: "count", Session: first}); w.State != store.Failed || !strings.Contains(w.Error, "neither session/resume nor session/load") {
		t.Fatalf("--session %s: %s %s %q (want failed, with the reason)", first, w.State, w.ErrorCode, w.Error)
	}
}

package store

import (
	"testing"
	"time"
)

// A thread is failed while its failures are not followed by a turn that ended otherwise (done, or
// stopped by a person): one failure answered by a turn that went well no longer marks it.
func TestConversationFailedSinceTheLastGoodTurn(t *testing.T) {
	s := openTest(t, Options{})
	turn := func(thread string, state State) string {
		t.Helper()
		m := msg("agent:r", "x")
		m.Thread = thread
		got := mustInsert(t, s, m)
		time.Sleep(3 * time.Millisecond)
		if state == Cancelled {
			if _, err := s.Cancel(ctx, got.ID); err != nil {
				t.Fatal(err)
			}
			return got.ID
		}
		c, _, err := s.Claim(ctx, "agent:r", time.Minute)
		if err != nil || c.ID != got.ID {
			t.Fatalf("claim: %+v %v", c, err)
		}
		code := ""
		if state == Failed {
			code = "agent_error"
		}
		if err := s.Complete(ctx, c.ID, c.LeaseID, state, "", code, code); err != nil {
			t.Fatal(err)
		}
		return got.ID
	}
	failed := func(thread string) int {
		t.Helper()
		page, _, err := s.Conversations(ctx, nil, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range page {
			if c.Thread == thread {
				return c.Failed
			}
		}
		t.Fatalf("no thread %s", thread)
		return 0
	}
	root := turn("", Failed)
	if failed(root) != 1 {
		t.Fatal("its only turn failed: failed")
	}
	turn(root, Done)
	if got := failed(root); got != 0 {
		t.Fatalf("a turn went well after the failure: %d, not failed", got)
	}
	turn(root, Failed)
	turn(root, Failed)
	if got := failed(root); got != 2 {
		t.Fatalf("two failures since the last good turn: %d", got)
	}
	turn(root, Cancelled)
	if got := failed(root); got != 0 {
		t.Fatalf("stopped by a person after them: %d", got)
	}
}

// What a party cannot see changes nothing it is told: a turn that went well, sent to an agent the
// party does not see, does not clear the failure it sees.
func TestConversationFailedIsOfWhatThePartySees(t *testing.T) {
	s := openTest(t, Options{})
	f := mustInsert(t, s, msg("agent:pub", "x"))
	c, _, _ := s.Claim(ctx, "agent:pub", time.Minute)
	if err := s.Complete(ctx, c.ID, c.LeaseID, Failed, "", "agent_error", "boom"); err != nil {
		t.Fatal(err)
	}
	hidden := msg("agent:private", "y")
	hidden.Thread = f.ID
	mustInsert(t, s, hidden)
	h, _, _ := s.Claim(ctx, "agent:private", time.Minute)
	if err := s.Complete(ctx, h.ID, h.LeaseID, Done, "ok", "", ""); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Conversations(ctx, &Party{Sender: "client:x", Recipients: []string{"agent:pub"}}, "", 10)
	if len(got) != 1 || got[0].Failed != 1 || got[0].Messages != 1 {
		t.Fatalf("%+v", got)
	}
	all, _, _ := s.Conversations(ctx, nil, "", 10)
	if len(all) != 1 || all[0].Failed != 0 {
		t.Fatalf("the node's user sees the turn that went well: %+v", all)
	}
}

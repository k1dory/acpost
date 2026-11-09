package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The final usage event carries the fullest reading of the turn that is counted, not what the last
// attempt read: a node receiving it counts that too.
func TestFinalUsageEventIsTheCountedReading(t *testing.T) {
	ctx := context.Background()
	open := func() *Store {
		s, err := Open("memory:", Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	s := open()
	m := Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"}
	if _, _, err := s.Insert(ctx, &m); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.Claim(ctx, "agent:x", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	reading := func(input int) string {
		b, _ := json.Marshal(Usage{Turn: "sbu_1", Agent: "agent:x@n", Model: "m", Reported: true, Input: input, Total: input})
		return string(b)
	}
	if err := s.PutUsage(ctx, m.ID, c.LeaseID, reading(150), false); err != nil { // an attempt read 150, broke, retried
		t.Fatal(err)
	}
	if err := s.PutUsage(ctx, m.ID, c.LeaseID, reading(100), true); err != nil { // the last broke earlier
		t.Fatal(err)
	}
	evs, err := s.Events(ctx, m.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var data []string
	for _, e := range evs {
		if e.Kind == "usage" {
			data = append(data, e.Data)
		}
	}
	if len(data) != 1 {
		t.Fatalf("usage events %q, want one", data)
	}
	var u Usage
	if err := json.Unmarshal([]byte(data[0]), &u); err != nil || u.Input != 150 {
		t.Fatalf("the event says %d input tokens (%v), want the counted 150", u.Input, err)
	}
	// The sender's node, given the event, counts what it says.
	r := open()
	rm := Message{ID: m.ID, From: "user:a", To: "agent:x@n", Body: "hi"}
	if _, _, err := r.Insert(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	if err := r.AddEvent(ctx, m.ID, "usage", data[0]); err != nil {
		t.Fatal(err)
	}
	sums, err := r.UsageOf(ctx, UsageQuery{})
	if err != nil || len(sums) != 1 || sums[0].Input != 150 {
		t.Fatalf("the receiving node counts %+v %v, want 150", sums, err)
	}
}

// Whatever makes a message final publishes the usage an attempt counted without an event (it was
// retried): in the same write, so a cancel between attempts, an expiry, a reaped or interrupted last
// attempt or a hub's outcome does not leave it to this node alone.
func TestEveryFinalizerPublishesCountedUsage(t *testing.T) {
	ctx := context.Background()
	usage := func() string {
		b, _ := json.Marshal(Usage{Turn: "sbu_" + NewID(), Agent: "agent:x@n", Model: "m", Reported: true, Input: 100, Total: 100})
		return string(b)
	}
	// running: a message claimed, its attempt's usage counted without an event; queued: then released.
	running := func(t *testing.T, s *Store, m *Message) Message {
		if _, _, err := s.Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		c, ok, err := s.Claim(ctx, "agent:x", time.Minute)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		if err := s.PutUsage(ctx, c.ID, c.LeaseID, usage(), false); err != nil {
			t.Fatal(err)
		}
		return c
	}
	queued := func(t *testing.T, s *Store, m *Message) Message {
		c := running(t, s, m)
		if st, err := s.Release(ctx, c.ID, c.LeaseID, "agent_unavailable", "broke", 5); err != nil || st != Queued {
			t.Fatal(st, err)
		}
		return c
	}
	for _, tc := range []struct {
		name string
		end  func(t *testing.T, s *Store) string
	}{
		{"complete", func(t *testing.T, s *Store) string {
			c := running(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"})
			return c.ID + must(t, s.Complete(ctx, c.ID, c.LeaseID, Done, "ok", "", ""))
		}},
		{"last release", func(t *testing.T, s *Store) string {
			c := running(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"})
			st, err := s.Release(ctx, c.ID, c.LeaseID, "agent_unavailable", "broke", 1)
			if st != Failed {
				t.Fatalf("release: %s", st)
			}
			return c.ID + must(t, err)
		}},
		{"cancel running", func(t *testing.T, s *Store) string {
			c := running(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"})
			_, err := s.Cancel(ctx, c.ID)
			return c.ID + must(t, err)
		}},
		{"cancel between attempts", func(t *testing.T, s *Store) string {
			c := queued(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"})
			_, err := s.Cancel(ctx, c.ID)
			return c.ID + must(t, err)
		}},
		{"expire between attempts", func(t *testing.T, s *Store) string {
			c := queued(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi", ExpiresAt: time.Now().Add(time.Hour)})
			if n, err := s.Expire(ctx, time.Now().Add(2*time.Hour)); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			return c.ID
		}},
		{"reaped last attempt", func(t *testing.T, s *Store) string {
			c := running(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"})
			if n, err := s.ReapLeases(ctx, time.Now().Add(2*time.Minute), 1); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			return c.ID
		}},
		{"interrupted last attempt", func(t *testing.T, s *Store) string {
			c := running(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"})
			if n, err := s.RequeueRunning(ctx, []string{"agent:x"}, 1); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			return c.ID
		}},
		{"settled", func(t *testing.T, s *Store) string {
			c := running(t, s, &Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"})
			return c.ID + must(t, s.Settle(ctx, c.ID, Done, "ok", "", "", 1))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open("memory:", Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			id := tc.end(t, s)
			if m, err := s.Get(ctx, id); err != nil || !m.State.Final() {
				t.Fatalf("not final: %s %v", m.State, err)
			}
			evs, err := s.Events(ctx, id, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var got []Usage
			for _, e := range evs {
				if e.Kind == "usage" {
					u, ok := readUsage(e.Data)
					if !ok {
						t.Fatalf("unreadable usage event %s", e.Data)
					}
					got = append(got, u)
				}
			}
			if len(got) != 1 || got[0].Input != 100 || !got[0].Reported || got[0].Agent != "agent:x@n" {
				t.Fatalf("usage events %+v: want one of 100 input tokens", got)
			}
			// The message's count of events took the new one: the next event gets a number of its own.
			if err := s.AddEvent(ctx, id, "notice", `{"text":"after"}`); err != nil {
				t.Fatalf("an event after the usage: %v", err)
			}
		})
	}
}

// must is "" when err is nil (and fails the test otherwise).
func must(t *testing.T, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return ""
}

// usageOf is a usage event's data for a turn of n input tokens.
func usageOf(turn string, n int) string {
	b, _ := json.Marshal(Usage{Turn: turn, Agent: "agent:x@n", Model: "m", Reported: true, Input: n, Total: n})
	return string(b)
}

// claimedWithUsage is a running message of agent:x whose attempt counted turns (no events).
func claimedWithUsage(t *testing.T, s *Store, turns map[string]int) Message {
	t.Helper()
	ctx := context.Background()
	m := Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"}
	if _, _, err := s.Insert(ctx, &m); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.Claim(ctx, "agent:x", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	for turn, n := range turns {
		if err := s.PutUsage(ctx, c.ID, c.LeaseID, usageOf(turn, n), false); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func usageEvents(t *testing.T, s *Store, id string) []Usage {
	t.Helper()
	evs, err := s.Events(context.Background(), id, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []Usage
	for _, e := range evs {
		if e.Kind == "usage" {
			u, _ := readUsage(e.Data)
			out = append(out, u)
		}
	}
	return out
}

// The trigger publishes no more than the room left for usage events: 15 there, two turns counted
// without one, one more goes (16 in all).
func TestFinalUsageKeepsToItsRoom(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{})
	c := claimedWithUsage(t, s, map[string]int{"late_a": 5, "late_b": 6})
	for i := 0; i < 15; i++ {
		if err := s.AddEvent(ctx, c.ID, "usage", usageOf(fmt.Sprintf("early_%02d", i), 1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Cancel(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := usageEvents(t, s, c.ID); len(got) != maxUsageEvents {
		t.Fatalf("%d usage events, want %d", len(got), maxUsageEvents)
	}
	if err := s.AddEvent(ctx, c.ID, "notice", `{"text":"after"}`); err != nil {
		t.Fatalf("an event after: %v", err)
	}
}

// A final message's count stays the one its event published: a later, fuller reading (a cancelled
// attempt's last word) does not move the count away from the event.
func TestFinalCountIsThePublishedOne(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{})
	c := claimedWithUsage(t, s, map[string]int{"t1": 100})
	if _, err := s.Cancel(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUsage(ctx, c.ID, "", usageOf("t1", 150), true); err != nil {
		t.Fatal(err)
	}
	got := usageEvents(t, s, c.ID)
	sums, err := s.UsageOf(ctx, UsageQuery{})
	if err != nil || len(got) != 1 || len(sums) != 1 || int64(got[0].Input) != sums[0].Input {
		t.Fatalf("event %+v, counted %+v %v: the event and the count must agree", got, sums, err)
	}
}

// A store opened over one whose trigger an earlier version installed gets the current trigger.
func TestUsageTriggerReplacedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s", "store.db")
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER usage_published_when_final;
		CREATE TRIGGER usage_published_when_final AFTER UPDATE OF state ON messages BEGIN SELECT 1; END;`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err = Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var body string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'usage_published_when_final'").Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "INSERT INTO events") {
		t.Fatalf("the trigger is still the old one: %s", body)
	}
}

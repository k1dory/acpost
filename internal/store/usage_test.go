package store

import (
	"encoding/json"
	"testing"
	"time"
)

func usageEvent(t *testing.T, s *Store, msg string, u Usage) {
	t.Helper()
	b, _ := json.Marshal(u)
	if err := s.AddEvent(ctx, msg, "usage", string(b)); err != nil {
		t.Fatal(err)
	}
}

func costOf(v float64) *float64 { return &v }

// A turn is counted once however often its event comes; turns are summed by group, the tokens of
// those without a cost kept per model for an estimate.
func TestUsageSums(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	t1 := Usage{Turn: "t1", At: time.Now(), Agent: "agent:a@n", Session: "s1", Model: "Opus", Reported: true, Input: 10, Output: 5, CacheRead: 90, Cost: costOf(1), Currency: "USD"}
	usageEvent(t, s, m.ID, t1)
	usageEvent(t, s, m.ID, t1) // again: a retry, a copy by another way
	usageEvent(t, s, m.ID, Usage{Turn: "t2", At: time.Now(), Agent: "agent:a@n", Session: "s1", Model: "Opus", Reported: true, Input: 20, Output: 5, CacheRead: 80, Cost: costOf(0.5), Currency: "USD"})
	usageEvent(t, s, m.ID, Usage{Turn: "t3", At: time.Now(), Agent: "agent:b@n", Model: "gpt", Reported: true, Input: 100, Output: 1, CacheRead: 300})
	usageEvent(t, s, m.ID, Usage{Agent: "agent:b@n", Input: 7}) // no turn id: not a turn's usage

	all, err := s.UsageOf(ctx, UsageQuery{By: "agent"})
	if err != nil || len(all) != 2 {
		t.Fatalf("%+v %v", all, err)
	}
	a, b := all[0], all[1]
	if a.Key != "agent:a@n" || a.Turns != 2 || a.Input != 30 || a.CacheRead != 170 || a.Cost != 1.5 || a.Costed != 2 || a.Currency != "USD" || len(a.Uncosted) != 0 {
		t.Fatalf("a: %+v", a)
	}
	if b.Turns != 1 || b.Costed != 0 || b.Uncosted["gpt"].Input != 100 || b.Uncosted["gpt"].CacheRead != 300 {
		t.Fatalf("b: %+v", b)
	}
	if _, err := s.UsageOf(ctx, UsageQuery{By: "nope"}); err == nil {
		t.Fatal("bad by accepted")
	}
}

// A turn counts on the day it ran, not the day its event arrived (a node catching up after a
// restart); a time from the future is not believed.
func TestUsageCountsWhenTheTurnRan(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	yesterday := time.Now().Add(-24 * time.Hour)
	usageEvent(t, s, m.ID, Usage{Turn: "old", At: yesterday, Agent: "agent:a@n", Input: 1})
	usageEvent(t, s, m.ID, Usage{Turn: "future", At: time.Now().Add(48 * time.Hour), Agent: "agent:a@n", Input: 1})
	days, _ := s.UsageOf(ctx, UsageQuery{By: "day"})
	if len(days) != 2 || days[0].Key != yesterday.Format(time.DateOnly) || days[1].Key != time.Now().Format(time.DateOnly) {
		t.Fatalf("%+v", days)
	}
	if got, _ := s.UsageOf(ctx, UsageQuery{From: time.Now().Add(-time.Hour)}); len(got) != 1 || got[0].Turns != 1 {
		t.Fatalf("%+v", got)
	}
}

// A turn's cost is how much its session's cumulative cost grew; a reset starts over. A session's
// first turn here counts from the base when it is known, else its cost is not known.
func TestTurnCost(t *testing.T) {
	s := openTest(t, Options{})
	zero := 0.0
	for i, c := range []struct {
		cumulative, want float64
		base             *float64
	}{{1, 1, &zero}, {1.5, 0.5, nil}, {1.5, 0, nil}, {0.2, 0.2, nil}, {0.7, 0.5, nil}} {
		got, ok, err := s.TurnCost(ctx, "agent:a@n", "s1", "USD", c.cumulative, c.base)
		if err != nil || !ok || got < c.want-1e-9 || got > c.want+1e-9 {
			t.Fatalf("step %d: %v %v %v, want %v", i, got, ok, err, c.want)
		}
	}
	// A session taken up in the middle, its cost before not known: the first turn has no cost,
	// the next counts from it.
	if _, ok, _ := s.TurnCost(ctx, "agent:a@n", "old", "USD", 100, nil); ok {
		t.Fatal("the history of a session billed to its first turn here")
	}
	if got, ok, _ := s.TurnCost(ctx, "agent:a@n", "old", "USD", 101, nil); !ok || got != 1 {
		t.Fatalf("next turn: %v %v", got, ok)
	}
	// Loaded at 100: the first turn costs what it added.
	loaded := 100.0
	if got, ok, _ := s.TurnCost(ctx, "agent:a@n", "loaded", "USD", 101.5, &loaded); !ok || got != 1.5 {
		t.Fatalf("loaded: %v %v", got, ok)
	}
}

// A usage event come again (a replay after a restart) takes no place of the few a message keeps.
func TestUsageReplayTakesNoPlace(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	for range maxUsageEvents + 4 {
		usageEvent(t, s, m.ID, Usage{Turn: "same", At: time.Now(), Agent: "agent:a@n", Input: 1})
	}
	usageEvent(t, s, m.ID, Usage{Turn: "new", At: time.Now(), Agent: "agent:a@n", Input: 1})
	if got, _ := s.UsageOf(ctx, UsageQuery{}); got[0].Turns != 2 {
		t.Fatalf("%d turns", got[0].Turns)
	}
}

// Usage events are kept past the event limit, so they reach the other nodes, but only a few.
func TestUsagePastTheEventLimit(t *testing.T) {
	s := openTest(t, Options{MaxEventsPerMessage: 3})
	m := mustInsert(t, s, msg("agent:r", "x"))
	for range 5 {
		if err := s.AddEvent(ctx, m.ID, "tool_call", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	for i := range maxUsageEvents + 3 {
		usageEvent(t, s, m.ID, Usage{Turn: "t" + string(rune('a'+i)), At: time.Now(), Agent: "agent:a@n", Input: 1})
	}
	evs, _ := s.Events(ctx, m.ID, 0, 100)
	usage := 0
	for _, e := range evs {
		if e.Kind == "usage" {
			usage++
		}
	}
	if usage != maxUsageEvents {
		t.Fatalf("%d usage events kept", usage)
	}
	if got, _ := s.UsageOf(ctx, UsageQuery{}); got[0].Turns != maxUsageEvents {
		t.Fatalf("%d turns counted", got[0].Turns)
	}
}

// Usage outlives its message, until UsageRetention.
func TestUsageOutlivesMessages(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	usageEvent(t, s, m.ID, Usage{Turn: "t", At: time.Now(), Agent: "agent:a@n", Reported: true, Input: 1})
	if _, err := s.Cancel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GC(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, m.ID); err == nil {
		t.Fatal("message not collected")
	}
	if got, _ := s.UsageOf(ctx, UsageQuery{}); len(got) != 1 || got[0].Turns != 1 {
		t.Fatalf("%+v", got)
	}
}

// Threads, the most recently active first, a page at a time; a party sees its own.
func TestConversations(t *testing.T) {
	s := openTest(t, Options{})
	a := mustInsert(t, s, msg("agent:a", "first line of a\nmore"))
	time.Sleep(3 * time.Millisecond)
	b := mustInsert(t, s, msg("agent:b", "b"))
	time.Sleep(3 * time.Millisecond)
	c := mustInsert(t, s, msg("agent:c", "c"))
	time.Sleep(3 * time.Millisecond)
	if _, err := s.Cancel(ctx, a.ID); err != nil { // a, changed last, comes first
		t.Fatal(err)
	}
	page, next, err := s.Conversations(ctx, nil, "", 2)
	if err != nil || len(page) != 2 || page[0].Thread != a.ID || page[1].Thread != c.ID || next == "" {
		t.Fatalf("%+v %q %v", page, next, err)
	}
	if page[0].Title != "first line of a" || page[0].Agents[0] != "agent:a" || page[0].Messages != 1 {
		t.Fatalf("%+v", page[0])
	}
	rest, next, _ := s.Conversations(ctx, nil, next, 2)
	if len(rest) != 1 || rest[0].Thread != b.ID || next != "" {
		t.Fatalf("%+v %q", rest, next)
	}
	mine, _, _ := s.Conversations(ctx, &Party{Sender: "client:x", Recipients: []string{"agent:b"}}, "", 10)
	if len(mine) != 1 || mine[0].Thread != b.ID {
		t.Fatalf("%+v", mine)
	}
}

// A party that is only in a later message of a thread gets its title from that message, not the
// root it may not see.
func TestConversationTitleVisible(t *testing.T) {
	s := openTest(t, Options{})
	root := mustInsert(t, s, msg("agent:private", "secret plans"))
	later := msg("agent:pub", "public step")
	later.Thread = root.ID
	mustInsert(t, s, later)
	got, _, _ := s.Conversations(ctx, &Party{Sender: "client:x", Recipients: []string{"agent:pub"}}, "", 10)
	if len(got) != 1 || got[0].Title != "public step" {
		t.Fatalf("%+v", got)
	}
	// Acting as agent:a sees what went to it, not what it sent to another agent.
	sent := msg("agent:b2", "from a to b2")
	sent.From = "agent:a"
	mustInsert(t, s, sent)
	got, _, _ = s.Conversations(ctx, &Party{Sender: "client:x", Recipients: []string{"agent:a"}}, "", 10)
	if len(got) != 0 {
		t.Fatalf("an agent's own sends shown to who acts as it: %+v", got)
	}
}

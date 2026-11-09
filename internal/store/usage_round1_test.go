package store

import (
	"strings"
	"testing"
	"time"
)

// Usage that cannot be read is not dropped silently: a notice says so, and it is not counted.
func TestUnreadableUsageLeavesANotice(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	if err := s.AddEvent(ctx, m.ID, "usage", `{"broken":`); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Events(ctx, m.ID, 0, 100)
	if err != nil || len(evs) != 1 || evs[0].Kind != "notice" || !strings.Contains(evs[0].Data, "usage") {
		t.Fatalf("%+v %v", evs, err)
	}
	if got, _ := s.UsageOf(ctx, UsageQuery{}); len(got) != 0 {
		t.Fatalf("counted: %+v", got)
	}
}

// Usage in the last place an event has still leaves the marker saying later ones were dropped.
func TestUsageInTheLastPlaceKeepsTheMarker(t *testing.T) {
	s := openTest(t, Options{MaxEventsPerMessage: 3})
	m := mustInsert(t, s, msg("agent:r", "x"))
	for range 2 {
		if err := s.AddEvent(ctx, m.ID, "tool_call", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	usageEvent(t, s, m.ID, Usage{Turn: "t1", At: time.Now(), Agent: "agent:a@n", Input: 1})
	for range 3 {
		if err := s.AddEvent(ctx, m.ID, "tool_call", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := s.Events(ctx, m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for i, e := range evs {
		kinds = append(kinds, e.Kind)
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
	}
	if got := strings.Join(kinds, " "); got != "tool_call tool_call usage truncated" {
		t.Fatalf("events: %s", got)
	}
}

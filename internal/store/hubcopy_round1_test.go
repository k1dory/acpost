package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A node copies a hub's events one by one and resumes from its own count after a restart. With
// a smaller limit than the hub's, that count may lag the hub (what is dropped is dropped again)
// but never lead it: a usage event in the last place followed by the marker would skip the hub's
// next event, the next attempt's usage among them.
func TestCopiedEventsNeverRunAhead(t *testing.T) {
	s := openTest(t, Options{MaxEventsPerMessage: 3})
	m := mustInsert(t, s, msg("agent:r@hub", "x"))
	usage := func(turn string) string {
		b, _ := json.Marshal(Usage{Turn: turn, At: time.Now(), Agent: "agent:r@hub", Input: 1})
		return string(b)
	}
	hub := []Event{{Kind: "tool_call", Data: "{}"}, {Kind: "tool_call", Data: "{}"}, {Kind: "usage", Data: usage("t1")},
		{Kind: "usage", Data: usage("t2")}, {Kind: "tool_call", Data: "{}"}, {Kind: "usage", Data: usage("t3")}}
	for i, e := range hub {
		if err := s.AddEvent(ctx, m.ID, e.Kind, e.Data); err != nil {
			t.Fatal(err)
		}
		// A restart here resumes from the local count.
		if after, err := s.EventCount(ctx, m.ID); err != nil || after > i+1 {
			t.Fatalf("after %d hub events the local count is %d (%v): hub event %d would be skipped", i+1, after, err, i+2)
		}
	}
	if got, _ := s.UsageOf(ctx, UsageQuery{}); len(got) != 1 || got[0].Turns != 3 {
		t.Fatalf("usage counted: %+v", got)
	}
	evs, _ := s.Events(ctx, m.ID, 0, 100)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, " "); got != "tool_call tool_call usage usage truncated usage" {
		t.Fatalf("events: %s", got)
	}
}

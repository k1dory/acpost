package store

import (
	"context"
	"encoding/json"
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

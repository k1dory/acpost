package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// X13: an event of a turn is taken only under the lease that runs the message now: an attempt
// whose lease was reaped and taken again by another is refused.
func TestTurnEventUnderTheCurrentLease(t *testing.T) {
	s, err := Open("memory:", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	m := Message{ID: NewID(), From: "user:a", To: "agent:x", Body: "hi"}
	if _, _, err := s.Insert(ctx, &m); err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.Claim(ctx, "agent:x", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.AddTurnEventLeased(ctx, m.ID, first.LeaseID, "text", `{"text":"one"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReapLeases(ctx, time.Now().Add(2*time.Minute), 5); err != nil {
		t.Fatal(err)
	}
	second, ok, err := s.Claim(ctx, "agent:x", time.Minute)
	if err != nil || !ok || second.LeaseID == first.LeaseID {
		t.Fatal(ok, err)
	}
	if err := s.AddTurnEventLeased(ctx, m.ID, first.LeaseID, "text", `{"text":"late"}`); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("the reaped attempt's event: %v", err)
	}
	if err := s.AddTurnEventLeased(ctx, m.ID, second.LeaseID, "text", `{"text":"two"}`); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Events(ctx, m.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs {
		got = append(got, e.Data)
	}
	if len(got) != 2 || got[0] != `{"text":"one"}` || got[1] != `{"text":"two"}` {
		t.Fatalf("events %q", got)
	}
}

package store

import (
	"errors"
	"sync"
	"testing"
)

// Disown finds a message by its id or its sender's key; otherwise the id is refused from then on,
// and only the id: a send again with the key, under a new id, is taken.
func TestDisown(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, &Message{ID: NewID(), From: "user:u@edge", To: "agent:bot", Body: "b", IdemKey: "k"})
	if got, found, err := s.Disown(ctx, "user:u@edge", m.ID, ""); err != nil || !found || got.ID != m.ID {
		t.Fatalf("by id: %v %v", found, err)
	}
	if got, found, err := s.Disown(ctx, "user:u@edge", NewID(), "k"); err != nil || !found || got.ID != m.ID {
		t.Fatalf("by key: %v %v", found, err)
	}
	id := NewID()
	if _, found, err := s.Disown(ctx, "user:u@edge", id, "k2"); err != nil || found {
		t.Fatalf("absent: %v %v", found, err)
	}
	if _, _, err := s.Insert(ctx, &Message{ID: id, From: "user:u@edge", To: "agent:bot", Body: "late", IdemKey: "k2"}); !errors.Is(err, ErrDisowned) {
		t.Fatalf("the disowned send was taken: %v", err)
	}
	if _, created, err := s.Insert(ctx, &Message{ID: NewID(), From: "user:u@edge", To: "agent:bot", Body: "again", IdemKey: "k2"}); err != nil || !created {
		t.Fatalf("a send again with the key: %v %v", created, err)
	}
}

// A disown and a late send of the same id at once: exactly one of them wins, never both.
func TestDisownRacesSend(t *testing.T) {
	s := openTest(t, Options{})
	for range 20 {
		id := NewID()
		var wg sync.WaitGroup
		var found bool
		var insErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, found, _ = s.Disown(ctx, "user:u@edge", id, "") }()
		go func() {
			defer wg.Done()
			_, _, insErr = s.Insert(ctx, &Message{ID: id, From: "user:u@edge", To: "agent:bot", Body: "b"})
		}()
		wg.Wait()
		if found == (insErr != nil) {
			t.Fatalf("disown found %v, the send's error %v", found, insErr)
		}
	}
}

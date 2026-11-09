package store

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func hubUsage(turn string) string {
	b, _ := json.Marshal(Usage{Turn: turn, At: time.Now(), Agent: "agent:r@hub", Input: 1})
	return string(b)
}

// copyFrom copies the hub's events after HubCursor onto the local copy, up to the hub's event
// numbered upto, as relayevents.go followHub does.
func copyFrom(t *testing.T, hub, local *Store, id string, upto int64) {
	t.Helper()
	after, err := local.HubCursor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := hub.Events(ctx, id, after, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Seq > upto {
			break
		}
		if err := local.CopyHubEvent(ctx, id, e.Seq, e.Kind, e.Data); err != nil {
			t.Fatal(err)
		}
	}
}

// The hub keeps more events than this node: a restart right after tool_call, tool_call, usage
// (the local copy now at its limit) resumes from the hub's number, and no later hub event, the
// next turn's usage among them, is skipped or taken twice.
func TestHubCursorAcrossARestart(t *testing.T) {
	hub := openTest(t, Options{MaxEventsPerMessage: 100})
	path := t.TempDir() + "/s/local.db"
	local, err := Open("sqlite://"+path, Options{MaxEventsPerMessage: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := mustInsert(t, hub, msg("agent:r@hub", "x"))
	c := m
	if _, _, err := local.Insert(ctx, &c); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{{Kind: "tool_call", Data: "{}"}, {Kind: "tool_call", Data: "{}"}, {Kind: "usage", Data: hubUsage("t1")},
		{Kind: "usage", Data: hubUsage("t2")}, {Kind: "tool_call", Data: "{}"}, {Kind: "tool_call", Data: "{}"}, {Kind: "usage", Data: hubUsage("t3")}} {
		if err := hub.AddEvent(ctx, m.ID, e.Kind, e.Data); err != nil {
			t.Fatal(err)
		}
	}
	copyFrom(t, hub, local, m.ID, 3)
	if got, err := local.HubCursor(ctx, m.ID); err != nil || got != 3 {
		t.Fatalf("cursor after three hub events: %d %v", got, err)
	}
	local.Close()
	if local, err = Open("sqlite://"+path, Options{MaxEventsPerMessage: 3}); err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	copyFrom(t, hub, local, m.ID, 7)
	copyFrom(t, hub, local, m.ID, 7) // a second follower, or a retry: nothing new
	if got, err := local.HubCursor(ctx, m.ID); err != nil || got != 7 {
		t.Fatalf("cursor after the hub's events: %d %v", got, err)
	}
	if got, _ := local.UsageOf(ctx, UsageQuery{}); len(got) != 1 || got[0].Turns != 3 {
		t.Fatalf("usage counted: %+v", got)
	}
	evs, _ := local.Events(ctx, m.ID, 0, 100)
	usage := 0
	for _, e := range evs {
		if e.Kind == "usage" {
			usage++
		}
	}
	if usage != 3 {
		t.Fatalf("usage events kept: %d of %+v", usage, evs)
	}
}

// Two followers copying the same hub events at once take each once.
func TestHubCursorConcurrentCopies(t *testing.T) {
	local := openTest(t, Options{MaxEventsPerMessage: 50})
	m := mustInsert(t, local, msg("agent:r@hub", "x"))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seq := int64(1); seq <= 20; seq++ {
				if err := local.CopyHubEvent(ctx, m.ID, seq, "tool_call", "{}"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if n, err := local.EventCount(ctx, m.ID); err != nil || n != 20 {
		t.Fatalf("events: %d %v", n, err)
	}
	if got, _ := local.HubCursor(ctx, m.ID); got != 20 {
		t.Fatalf("cursor: %d", got)
	}
}

// A copy made before the number was kept resumes from its count, as before; a message gone is
// not found.
func TestHubCursorBeforeTheColumn(t *testing.T) {
	path := t.TempDir() + "/s/s.db"
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := mustInsert(t, s, msg("agent:r@hub", "x"))
	for range 2 {
		if err := s.AddEvent(ctx, m.ID, "tool_call", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.Exec("ALTER TABLE messages DROP COLUMN hub_seq"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open("sqlite://"+path, Options{}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.HubCursor(ctx, m.ID); err != nil || got != 2 {
		t.Fatalf("cursor of an old copy: %d %v", got, err)
	}
	if _, err := s.HubCursor(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("gone: %v", err)
	}
	if err := s.CopyHubEvent(ctx, "nope", 1, "tool_call", "{}"); err != ErrNotFound {
		t.Fatalf("gone: %v", err)
	}
}

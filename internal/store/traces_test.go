package store

import (
	"testing"
	"time"
)

// Threads a person started are listed, the most recently active first; one an agent started
// (its message is under another's) is not.
func TestTraceThreads(t *testing.T) {
	s := openTest(t, Options{})
	a := mustInsert(t, s, msg("agent:a", "first"))
	time.Sleep(3 * time.Millisecond)
	b := mustInsert(t, s, msg("agent:a", "second"))
	time.Sleep(3 * time.Millisecond)
	sub := msg("agent:b", "asked by a")
	sub.Parent, sub.Depth = a.ID, 1
	mustInsert(t, s, sub)
	time.Sleep(3 * time.Millisecond)
	again := msg("agent:a", "more")
	again.Thread = a.ID
	mustInsert(t, s, again)

	got, err := s.TraceThreads(ctx, 10)
	if err != nil || len(got) != 2 || got[0] != a.ID || got[1] != b.ID {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := s.TraceThreads(ctx, 1); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("limit: %v", got)
	}
}

// A thread's messages for a trace: its last own ones, and those sent under them at any depth,
// bounded, in order, without bodies.
func TestTraceMessages(t *testing.T) {
	s := openTest(t, Options{})
	root := mustInsert(t, s, msg("agent:a", "root"))
	child := msg("agent:b", "child")
	child.Parent, child.Depth = root.ID, 1
	c := mustInsert(t, s, child)
	grand := msg("agent:c", "grandchild")
	grand.Parent, grand.Depth = c.ID, 2
	g := mustInsert(t, s, grand)
	next := msg("agent:a", "next")
	next.Thread = root.ID
	n := mustInsert(t, s, next)
	mustInsert(t, s, msg("agent:a", "another thread"))

	got, err := s.TraceMessages(ctx, root.ID, 10, 10)
	if err != nil || len(got) != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].ID != root.ID || got[1].ID != c.ID || got[2].ID != g.ID || got[3].ID != n.ID || got[0].Body != "" || got[2].Parent != c.ID {
		t.Fatalf("%+v", got)
	}
	// The last own message only: what was sent under the root is not.
	if got, _ := s.TraceMessages(ctx, root.ID, 1, 10); len(got) != 1 || got[0].ID != n.ID {
		t.Fatalf("own 1: %+v", got)
	}
	// Under the root, one more at most.
	if got, _ := s.TraceMessages(ctx, root.ID, 2, 1); len(got) != 3 {
		t.Fatalf("under 1: %+v", got)
	}
}

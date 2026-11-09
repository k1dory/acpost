package store

import (
	"context"
	"testing"
)

func TestSandboxCursorOnlyForward(t *testing.T) {
	s, err := Open("memory:", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if c, err := s.SandboxCursor(ctx, "agent:cloud", "u/sb1"); c != 0 || err != nil {
		t.Fatal(c, err)
	}
	for _, c := range []int64{40, 9, 120, 100} {
		if err := s.PutSandboxCursor(ctx, "agent:cloud", "u/sb1", c); err != nil {
			t.Fatal(err)
		}
	}
	if c, _ := s.SandboxCursor(ctx, "agent:cloud", "u/sb1"); c != 120 {
		t.Fatalf("cursor %d, want 120", c)
	}
	if c, _ := s.SandboxCursor(ctx, "agent:cloud", "u/sb2"); c != 0 {
		t.Fatalf("another sandbox's cursor %d", c)
	}
}

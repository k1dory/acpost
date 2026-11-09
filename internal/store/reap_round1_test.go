package store

import (
	"testing"
	"time"
)

// A message whose lease ran out and went back to the queue says why with a code, as Release does:
// error and error_code come together.
func TestReapedBackToTheQueueHasACode(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	if _, ok, err := s.Claim(ctx, "agent:r", time.Millisecond); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n, err := s.ReapLeases(ctx, time.Now().Add(time.Second), 3); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	got, err := s.Get(ctx, m.ID)
	if err != nil || got.State != Queued || got.Error != "lease lost" || got.ErrorCode != "lease_lost" {
		t.Fatalf("%+v %v", got, err)
	}
}

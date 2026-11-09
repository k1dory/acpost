package store

import (
	"testing"
	"time"
)

// A claim with no lease (or a negative one) is refused: the message would be reaped back to the
// queue at once, an attempt spent for nothing.
func TestClaimNeedsAPositiveLease(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:z", "x"))
	for _, lease := range []time.Duration{0, -time.Second} {
		if _, ok, err := s.Claim(ctx, "agent:z", lease); err == nil || ok {
			t.Fatalf("lease %v: ok=%v err=%v", lease, ok, err)
		}
	}
	if _, err := s.ReapLeases(ctx, time.Now().Add(time.Second), 3); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.Claim(ctx, "agent:z", time.Minute)
	if err != nil || !ok || c.Attempts != 1 {
		t.Fatalf("a real claim after the refused ones: %+v ok=%v %v", c, ok, err)
	}
}

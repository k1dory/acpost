package node

import (
	"context"
	"testing"
	"time"
)

// A turn has no deadline unless its agent sets turn_timeout: an agent may work for hours.
func TestTurnContextHasADeadlineOnlyWhenSet(t *testing.T) {
	ctx, cancel := turnContext(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("no turn_timeout: no deadline")
	}
	ctx2, cancel2 := turnContext(context.Background(), time.Hour)
	defer cancel2()
	if d, ok := ctx2.Deadline(); !ok || time.Until(d) < 59*time.Minute {
		t.Fatal("turn_timeout set: its deadline")
	}
}

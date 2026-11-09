package store

import (
	"strings"
	"testing"
	"time"
)

// A reader that keeps an old snapshot open stops the WAL from being reset. The checkpoint that
// tries is bounded: a send does not wait out busy_timeout (10 s) for that reader.
func TestCheckpointDoesNotHoldASend(t *testing.T) {
	s := openTest(t, Options{JournalSizeLimit: 256 << 10})
	mustInsert(t, s, msg("agent:r", "x"))
	rtx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rtx.Rollback()
	var n int
	if err := rtx.QueryRowContext(ctx, "SELECT count(*) FROM messages").Scan(&n); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("w", 64<<10)
	for i := range 8 {
		start := time.Now()
		mustInsert(t, s, msg("agent:r", body))
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("send %d took %v behind a reader", i, d)
		}
	}
	// The writer's own wait is back once the checkpoint is over.
	var wait int
	if err := s.w.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&wait); err != nil || wait != 10000 {
		t.Fatalf("busy_timeout after the checkpoint: %d %v", wait, err)
	}
}

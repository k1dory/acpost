package store

import (
	"testing"
	"time"
)

// A claim waiting for the writer reads the time once it has it: a message that expired while the
// claim waited (stored meanwhile, expiring between the claim's call and its turn) is not taken.
func TestClaimWaitingForWriterSkipsExpired(t *testing.T) {
	s := openTest(t, Options{})
	tx, err := s.w.BeginTx(ctx, nil) // the one writer, held
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		ok  bool
		err error
	}
	got := make(chan result, 1)
	go func() {
		_, ok, err := s.Claim(ctx, "agent:bot", time.Minute)
		got <- result{ok, err}
	}()
	time.Sleep(20 * time.Millisecond) // the claim is called, and waits
	now := time.Now()
	res, err := tx.ExecContext(ctx, `INSERT INTO messages (id, sender, recipient, thread, state, body_size, created_at, updated_at, expires_at)
		VALUES ('msg_x', 'user:u', 'agent:bot', 'msg_x', 'queued', 1, ?, ?, ?)`, ms(now), ms(now), ms(now.Add(5*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	seq, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO contents (seq, body) VALUES (?, 'b')`, seq); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond) // it expires before the writer is free
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := <-got; r.err != nil || r.ok {
		t.Fatalf("an expired message was claimed (%v)", r.err)
	}
}

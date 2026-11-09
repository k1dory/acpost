package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PutUsage records a turn's usage for message id. Its row in turn_usage is taken, or replaced by a
// fuller reading of the same turn (more tokens: an attempt that read the whole turn after one that
// broke off in it), never by a lesser one. With event, a "usage" event goes among the message's
// too: how the usage reaches the app and the sender's node. With a lease, all of it happens only
// while that attempt holds the message running (ErrLeaseLost otherwise), in one transaction with
// the check: what an attempt read of its turn is there before the attempt's outcome is.
func (s *Store) PutUsage(ctx context.Context, id, lease, data string, event bool) error {
	u, ok := readUsage(data)
	if !ok {
		return fmt.Errorf("store: not a turn's usage")
	}
	now := time.Now()
	return s.tx(ctx, func(t *sql.Tx) error {
		var seq int64
		var n int
		var state State
		var cur string
		err := t.QueryRowContext(ctx, "SELECT seq, events, state, lease_id FROM messages WHERE id = ?", id).Scan(&seq, &n, &state, &cur)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if lease != "" && (state != Running || cur != lease) {
			return ErrLeaseLost
		}
		var had int
		err = t.QueryRowContext(ctx, "SELECT total FROM turn_usage WHERE turn = ?", u.Turn).Scan(&had)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if event {
				_, err := s.insertEvent(ctx, t, id, seq, n, "usage", data) // counted there
				return err
			}
			return addUsage(ctx, t, id, u, now)
		case err != nil:
			return err
		}
		if u.Total > had {
			if _, err := t.ExecContext(ctx, `UPDATE turn_usage SET ts = ?, model = ?, reported = ?, input = ?, output = ?, cache_read = ?,
				cache_write = ?, thought = ?, total = ?, cost = ?, currency = ?, context_used = ?, context_size = ? WHERE turn = ?`,
				usageAt(u.At, now), u.Model, u.Reported, u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Thought, u.Total, u.Cost,
				u.Currency, u.ContextUsed, u.ContextSize, u.Turn); err != nil {
				return err
			}
		}
		if !event {
			return nil
		}
		// The turn is counted (an earlier reading of it): its event goes among the message's all the
		// same, as insertEvent keeps usage events (past the events' limit, up to maxUsageEvents).
		var have int
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE msg = ? AND kind = 'usage'", seq).Scan(&have); err != nil {
			return err
		}
		if have >= maxUsageEvents {
			return nil
		}
		if _, err := t.ExecContext(ctx, "INSERT INTO events (msg, seq, ts, kind, data) VALUES (?, ?, ?, ?, ?)", seq, n+1, ms(now), "usage", data); err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, "UPDATE messages SET events = events + 1 WHERE seq = ?", seq)
		return err
	})
}

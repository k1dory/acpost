package store

import (
	"context"
	"database/sql"
	"errors"
)

// AddTurnEventLeased records an event of a running turn under the lease of the attempt that runs
// it: once the message is final, or another attempt holds it (the lease reaped, the message taken
// again), the event is refused with ErrLeaseLost. A late word of an attempt that lost its lease
// does not land among the events of the one that runs now.
func (s *Store) AddTurnEventLeased(ctx context.Context, id, lease, kind, data string) error {
	if lease == "" {
		return ErrLeaseLost
	}
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
		if state != Running || cur != lease {
			return ErrLeaseLost
		}
		_, err = s.insertEvent(ctx, t, id, seq, n, kind, data)
		return err
	})
}

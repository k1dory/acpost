package store

import (
	"context"
	"database/sql"
	"errors"
)

// CopyHubEvent takes onto a forwarded message's local copy the hub's event numbered hubSeq, and
// records that number with it in one transaction: a copy resuming after a restart starts from
// HubCursor, not from its own count, which differs from the hub's whenever the two keep a
// different number of events (limits.max_events_per_message). An event at or below the number
// recorded was taken before (two followers, a retry) and is skipped.
func (s *Store) CopyHubEvent(ctx context.Context, id string, hubSeq int64, kind, data string) error {
	return s.tx(ctx, func(t *sql.Tx) error {
		var seq, cursor int64
		var n int
		err := t.QueryRowContext(ctx, "SELECT seq, events, hub_seq FROM messages WHERE id = ?", id).Scan(&seq, &n, &cursor)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || hubSeq <= cursor {
			return err
		}
		if _, err := s.insertEvent(ctx, t, id, seq, n, kind, data); err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, "UPDATE messages SET hub_seq = ? WHERE seq = ?", hubSeq, seq)
		return err
	})
}

// HubCursor is the number of the last hub event a forwarded message's copy took: where following
// the hub resumes. A copy that took none by number (made before the number was kept) resumes from
// its own count, as it did then: a hub event adds at most one event to the copy, so that count
// never runs ahead of the hub's (it may lag it, and what is taken again is skipped or dropped).
func (s *Store) HubCursor(ctx context.Context, id string) (int64, error) {
	var cursor, n int64
	err := s.r.QueryRowContext(ctx, "SELECT hub_seq, events FROM messages WHERE id = ?", id).Scan(&cursor, &n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if cursor == 0 {
		return n, err
	}
	return cursor, err
}

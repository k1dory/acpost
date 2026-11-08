package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Deliveries from a hub, on a joined node. A hub hands a message again under a new lease when it
// lost track of the last one (its lease ran out, a reply did not arrive): the node's copy is
// adopted in one step, the new generation and any change of state together, so a relay of the old
// generation cannot change the copy once the new one owns it.

// ErrIDInUse: the id is taken by a message that is not a copy of this delivery.
var ErrIDInUse = errors.New("the message id is in use by another message")

// CodeHubGaveUp marks a copy a node cancelled because its hub ended the message without it (the
// hub failed it after its lease was lost too often): a later delivery of it runs it again.
const CodeHubGaveUp = "lease_lost"

// AdoptDelivery takes a new delivery of message id onto the copy this node has: its origin
// becomes origin. Only a copy of the same message is taken (its origin starts with prefix, same
// sender and recipient): ErrIDInUse otherwise, ErrNotFound when there is no copy. A copy cancelled
// because the hub gave it up is queued again, its attempts kept; any other copy keeps its state (a
// copy running goes on; a final one is reported under the new lease).
func (s *Store) AdoptDelivery(ctx context.Context, id, from, to, prefix, origin string) (Message, error) {
	var m Message
	err := s.tx(ctx, func(t *sql.Tx) error {
		var sender, recipient, cur, code string
		var state State
		err := t.QueryRowContext(ctx, "SELECT sender, recipient, origin, state, error_code FROM messages WHERE id = ?", id).
			Scan(&sender, &recipient, &cur, &state, &code)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if len(cur) < len(prefix) || cur[:len(prefix)] != prefix || sender != from || recipient != to {
			return ErrIDInUse
		}
		now := ms(time.Now())
		if state == Cancelled && code == CodeHubGaveUp {
			if _, err := t.ExecContext(ctx, `UPDATE messages SET origin = ?, state = 'queued', error = '', error_code = '', reply_size = 0,
				lease_id = '', lease_until = 0, updated_at = ?, finished_at = 0 WHERE id = ?`, origin, now, id); err != nil {
				return err
			}
			if _, err := t.ExecContext(ctx, "UPDATE contents SET reply = '' WHERE seq = (SELECT seq FROM messages WHERE id = ?)", id); err != nil {
				return err
			}
		} else if _, err := t.ExecContext(ctx, "UPDATE messages SET origin = ? WHERE id = ?", origin, id); err != nil {
			return err
		}
		m, err = s.get(ctx, t, id)
		return err
	})
	return m, err
}

// CancelIfOrigin cancels a message that is not final, only while its origin is still origin (a
// relay of an old delivery cannot end a copy a new delivery owns). code and why say why ("" for a
// plain cancel). It reports whether it cancelled.
func (s *Store) CancelIfOrigin(ctx context.Context, id, origin, code, why string) (bool, error) {
	now := ms(time.Now())
	res, err := s.w.ExecContext(ctx, `UPDATE messages SET state = 'cancelled', error = ?, error_code = ?, lease_id = '', lease_until = 0,
		updated_at = ?, finished_at = ? WHERE id = ? AND origin = ? AND state IN ('queued', 'running')`, why, code, now, now, id, origin)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SetOriginIf changes a message's origin from old to origin, only if it is still old; it reports
// whether it did.
func (s *Store) SetOriginIf(ctx context.Context, id, old, origin string) (bool, error) {
	res, err := s.w.ExecContext(ctx, "UPDATE messages SET origin = ? WHERE id = ? AND origin = ?", origin, id, old)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// FinalOfOrigin lists, after the id after, final messages finished since since whose origin
// starts with prefix but not with acked, with their origins: a joined node's outcomes its hub has
// not confirmed taking.
func (s *Store) FinalOfOrigin(ctx context.Context, prefix, acked string, since time.Time, after string, limit int) ([][2]string, error) {
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, origin FROM messages WHERE origin <> '' AND substr(origin, 1, ?) = ? AND substr(origin, 1, ?) <> ?
		AND state IN ('done', 'failed', 'cancelled', 'expired') AND finished_at >= ? AND id > ? ORDER BY id LIMIT ?`,
		len(prefix), prefix, len(acked), acked, ms(since), after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var id, origin string
		if err := rows.Scan(&id, &origin); err != nil {
			return nil, err
		}
		out = append(out, [2]string{id, origin})
	}
	return out, rows.Err()
}

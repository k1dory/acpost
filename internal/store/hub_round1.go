package store

import (
	"context"
	"database/sql"
	"encoding/json"
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

// FinishForward makes a message stored before it was sent to a hub (origin forwarding) the
// local copy of what the hub took (sent): its origin becomes origin, and what the hub resolved
// (the recipient's node, its settings) is copied. It reports whether it did: not when the message
// is no longer a pending forward.
func (s *Store) FinishForward(ctx context.Context, id, forwarding, origin string, sent Message) (bool, error) {
	var meta string
	if len(sent.Meta) > 0 {
		b, _ := json.Marshal(sent.Meta)
		meta = string(b)
	}
	res, err := s.w.ExecContext(ctx, `UPDATE messages SET origin = ?, recipient = ?, task = ?, thread = ?, parent = ?, depth = ?, meta = ?,
		expires_at = ?, updated_at = ? WHERE id = ? AND origin = ? AND state IN ('queued', 'running') AND cancel_wanted = 0`,
		origin, sent.To, sent.Task, sent.Thread, sent.Parent, sent.Depth, meta, ms(sent.ExpiresAt), ms(time.Now()), id, forwarding)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DropForward removes a message stored before it was sent to a hub (origin forwarding), with
// what it holds: the hub kept another message for it (a repeat of an earlier send).
func (s *Store) DropForward(ctx context.Context, id, forwarding string) error {
	return s.tx(ctx, func(t *sql.Tx) error {
		var seq int64
		err := t.QueryRowContext(ctx, "SELECT seq FROM messages WHERE id = ? AND origin = ?", id, forwarding).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, q := range []string{"DELETE FROM events WHERE msg = ?", "DELETE FROM contents WHERE seq = ?", "DELETE FROM files WHERE msg = ?", "DELETE FROM messages WHERE seq = ?"} {
			if _, err := t.ExecContext(ctx, q, seq); err != nil {
				return err
			}
		}
		return dropOrphans(ctx, t)
	})
}

// ForwardNotSent marks a message stored to be sent to a hub (origin forwarding) as not sent for
// certain (the hub refused it, or never got it): its origin becomes unsent, a message not final
// yet fails with why, and its idempotency key is freed, so a send again with the key is sent
// rather than answered with this failure. It reports whether it did.
func (s *Store) ForwardNotSent(ctx context.Context, id, forwarding, unsent, why string) (bool, error) {
	now := ms(time.Now())
	// A cancel asked for meanwhile is what it ends as: nothing was sent to undo.
	res, err := s.w.ExecContext(ctx, `UPDATE messages SET origin = ?, idem_key = NULL,
		error = CASE WHEN state IN ('queued', 'running') AND cancel_wanted = 0 THEN ? ELSE error END,
		finished_at = CASE WHEN state IN ('queued', 'running') THEN ? ELSE finished_at END,
		state = CASE WHEN state NOT IN ('queued', 'running') THEN state WHEN cancel_wanted = 1 THEN 'cancelled' ELSE 'failed' END,
		cancel_wanted = 0, lease_id = '', lease_until = 0, updated_at = ? WHERE id = ? AND origin = ?`, unsent, why, now, now, id, forwarding)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// IdemKeyOf is a message's idempotency key ("" none).
func (s *Store) IdemKeyOf(ctx context.Context, id string) (string, error) {
	var key sql.NullString
	err := s.r.QueryRowContext(ctx, "SELECT idem_key FROM messages WHERE id = ?", id).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return key.String, err
}

// OfOrigin lists, after the id after, the ids of the messages whose origin is origin, in any state.
func (s *Store) OfOrigin(ctx context.Context, origin, after string, limit int) ([]string, error) {
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id FROM messages WHERE origin = ? AND id > ? ORDER BY id LIMIT ?`, origin, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ReplaceForward replaces, in one transaction, a message stored to be sent to a hub (id, origin
// forwarding) with a copy of the message the hub kept for the same key (first, with its files):
// the copy takes the sender's key, and a cancel asked for meanwhile stays with it, its origin
// forwarding until the cancel reaches the hub; otherwise its origin is origin. A copy already here
// takes the key and the cancel the same way. Nothing changes on any failure. It returns the copy.
func (s *Store) ReplaceForward(ctx context.Context, id, forwarding, origin string, first Message) (Message, error) {
	if first.ID == "" || first.To == "" {
		return Message{}, errors.New("store: replace needs the hub's message's id and recipient")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := s.tx(ctx, func(t *sql.Tx) error {
		var seq int64
		var sender string
		var state State
		var key sql.NullString
		var wanted bool
		err := t.QueryRowContext(ctx, "SELECT seq, sender, state, idem_key, cancel_wanted FROM messages WHERE id = ? AND origin = ?", id, forwarding).
			Scan(&seq, &sender, &state, &key, &wanted)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		for _, q := range []string{"DELETE FROM events WHERE msg = ?", "DELETE FROM contents WHERE seq = ?", "DELETE FROM files WHERE msg = ?", "DELETE FROM messages WHERE seq = ?"} {
			if _, err := t.ExecContext(ctx, q, seq); err != nil {
				return err
			}
		}
		// The dropped copy's files free their room before the copy that replaces it asks for room.
		if err := dropOrphans(ctx, t); err != nil {
			return err
		}
		var n int
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE id = ?", first.ID).Scan(&n); err != nil {
			return err
		}
		wanted = wanted || state.Final() // (a cancel stored by an earlier version: the copy was final)
		if n > 0 {
			// The hub's message is here already (an earlier send made it, without the key): it
			// takes the key, and the cancel asked for goes to the hub through it.
			if key.Valid {
				if _, err := t.ExecContext(ctx, "UPDATE messages SET idem_key = ? WHERE id = ? AND sender = ? AND idem_key IS NULL",
					key.String, first.ID, sender); err != nil {
					return err
				}
			}
			if wanted {
				if _, err := t.ExecContext(ctx, `UPDATE messages SET origin = ?, cancel_wanted = 1, updated_at = ?
					WHERE id = ? AND origin IN (?, ?) AND state IN ('queued', 'running')`, forwarding, ms(now), first.ID, origin, forwarding); err != nil {
					return err
				}
			}
		}
		if n == 0 {
			o, st, finished, cw := origin, Queued, int64(0), 0
			if wanted {
				o, cw = forwarding, 1
			}
			thread := first.Thread
			if thread == "" {
				thread = first.ID
			}
			var meta string
			if len(first.Meta) > 0 {
				b, _ := json.Marshal(first.Meta)
				meta = string(b)
			}
			var idem any
			if key.Valid {
				idem = key.String
			}
			if err := s.roomFor(ctx, t, int64(len(first.Body))); err != nil {
				return err
			}
			res, err := t.ExecContext(ctx, `INSERT INTO messages
				(id, sender, recipient, task, thread, parent, depth, state, meta, body_size, created_at, updated_at, expires_at, finished_at, origin, idem_key, cancel_wanted)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				first.ID, sender, first.To, first.Task, thread, first.Parent, first.Depth, st, meta, len(first.Body),
				ms(now), ms(now), ms(first.ExpiresAt), finished, o, idem, cw)
			if err != nil {
				return err
			}
			nseq, err := res.LastInsertId()
			if err != nil {
				return err
			}
			if _, err := t.ExecContext(ctx, "INSERT INTO contents (seq, body) VALUES (?, ?)", nseq, first.Body); err != nil {
				return err
			}
			if err := s.putFiles(ctx, t, nseq, false, first.Files); err != nil {
				return err
			}
		}
		return dropOrphans(ctx, t)
	})
	if err != nil {
		return Message{}, err
	}
	return s.Get(ctx, first.ID)
}

// ReopenForward takes back a cancel done here on a message stored to be sent to a hub, when the
// hub had already ended it (its outcome is the message's): queued again, with origin origin, for
// the hub's outcome to be copied onto it. It reports whether it did.
func (s *Store) ReopenForward(ctx context.Context, id, forwarding, origin string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE messages SET origin = ?, state = 'queued', error = '', error_code = '', finished_at = 0,
		updated_at = ? WHERE id = ? AND origin = ? AND state = 'cancelled'`, origin, ms(time.Now()), id, forwarding)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// WantCancel records a cancel asked for on a message stored to be sent to a hub (origin
// forwarding), not yet ended: it stays unfinished until the hub's verdict. It reports whether it
// did (false: no longer forwarding, or ended).
func (s *Store) WantCancel(ctx context.Context, id, forwarding string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE messages SET cancel_wanted = 1, updated_at = ?
		WHERE id = ? AND origin = ? AND state IN ('queued', 'running')`, ms(time.Now()), id, forwarding)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// CancelWanted says whether a cancel was asked for on a message and not yet settled.
func (s *Store) CancelWanted(ctx context.Context, id string) (bool, error) {
	var wanted bool
	err := s.r.QueryRowContext(ctx, "SELECT cancel_wanted FROM messages WHERE id = ?", id).Scan(&wanted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return wanted, err
}

// CancelSettled marks a message stored to be sent to a hub as the hub's (origin), its cancel
// request settled there (the hub cancelled it, or had ended it before): the hub's outcome is
// copied onto it next. It reports whether it did.
func (s *Store) CancelSettled(ctx context.Context, id, forwarding, origin string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE messages SET origin = ?, cancel_wanted = 0, updated_at = ? WHERE id = ? AND origin = ?`,
		origin, ms(time.Now()), id, forwarding)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PutUsage records a turn's usage for message id. Its row in turn_usage is taken, or replaced by a
// fuller reading of the same turn (more tokens: an attempt that read the whole turn after one that
// broke off in it), never by a lesser one. With event, the message's counted turns get their
// "usage" events too (PublishUsage), how the usage reaches the app and the sender's node. With a lease, all of it happens only
// while that attempt holds the message running (ErrLeaseLost otherwise), in one transaction with
// the check: what an attempt read of its turn is there before the attempt's outcome is.
func (s *Store) PutUsage(ctx context.Context, id, lease, data string, event bool) error {
	u, ok := readUsage(data)
	if !ok {
		return fmt.Errorf("store: not a turn's usage")
	}
	now := time.Now()
	return s.tx(ctx, func(t *sql.Tx) error {
		seq, n, final, err := leasedMessage(ctx, t, id, lease)
		if err != nil {
			return err
		}
		// A final message's count is the one its event published (as it turned final): a reading
		// after that (a cancelled attempt's last word) does not move it away from the event.
		if err := putUsageTx(ctx, t, id, u, now, !final); err != nil {
			return err
		}
		if event {
			return publishUsageTx(ctx, t, id, seq, n, now)
		}
		if final {
			return nil
		}
		return pendUsageTx(ctx, t, id, u.Turn)
	})
}

// PublishUsage puts among message id's events the usage of each of its turns that is counted and
// has no event yet (read by an attempt that was retried), from the counted row: before the message
// is final, so the app and the sender's node get it. With a lease, only while that attempt holds
// the message running (ErrLeaseLost otherwise).
func (s *Store) PublishUsage(ctx context.Context, id, lease string) error {
	now := time.Now()
	return s.tx(ctx, func(t *sql.Tx) error {
		seq, n, _, err := leasedMessage(ctx, t, id, lease)
		if err != nil {
			return err
		}
		return publishUsageTx(ctx, t, id, seq, n, now)
	})
}

// leasedMessage is a message's seq, event count and whether it is final, checked to be running
// under lease (when given).
func leasedMessage(ctx context.Context, t *sql.Tx, id, lease string) (int64, int, bool, error) {
	var seq int64
	var n int
	var state State
	var cur string
	err := t.QueryRowContext(ctx, "SELECT seq, events, state, lease_id FROM messages WHERE id = ?", id).Scan(&seq, &n, &state, &cur)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, ErrNotFound
	}
	if err != nil {
		return 0, 0, false, err
	}
	if lease != "" && (state != Running || cur != lease) {
		return 0, 0, false, ErrLeaseLost
	}
	return seq, n, state.Final(), nil
}

// putUsageTx counts a turn's usage, or (raise) replaces its count by a fuller reading of the same
// turn (more tokens), never by a lesser one.
func putUsageTx(ctx context.Context, t *sql.Tx, id string, u Usage, now time.Time, raise bool) error {
	var had int
	err := t.QueryRowContext(ctx, "SELECT total FROM turn_usage WHERE turn = ?", u.Turn).Scan(&had)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return addUsage(ctx, t, id, u, now)
	case err != nil:
		return err
	case !raise || u.Total <= had:
		return nil
	}
	_, err = t.ExecContext(ctx, `UPDATE turn_usage SET ts = ?, model = ?, reported = ?, input = ?, output = ?, cache_read = ?,
		cache_write = ?, thought = ?, total = ?, cost = ?, currency = ?, context_used = ?, context_size = ? WHERE turn = ?`,
		usageAt(u.At, now), u.Model, u.Reported, u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Thought, u.Total, u.Cost,
		u.Currency, u.ContextUsed, u.ContextSize, u.Turn)
	return err
}

// publishUsageTx adds a "usage" event for each counted turn of message id that has none: its data
// is the counted row (the fullest reading of the turn), not whatever an attempt read last. Usage
// events are kept past the events' limit, up to maxUsageEvents, as insertEvent keeps them.
func publishUsageTx(ctx context.Context, t *sql.Tx, id string, seq int64, n int, now time.Time) error {
	all, err := countedUsage(ctx, t, "msg = ?", id)
	if err != nil {
		return err
	}
	for _, u := range all {
		var have, same int
		if err := t.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(json_extract(data, '$.turn') = ?), 0) FROM events WHERE msg = ? AND kind = 'usage'",
			u.Turn, seq).Scan(&have, &same); err != nil {
			return err
		}
		if same > 0 || have >= maxUsageEvents {
			continue
		}
		b, err := json.Marshal(u)
		if err != nil {
			return err
		}
		n++
		if _, err := t.ExecContext(ctx, "INSERT INTO events (msg, seq, ts, kind, data) VALUES (?, ?, ?, ?, ?)", seq, n, ms(now), "usage", string(b)); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, "UPDATE messages SET events = events + 1 WHERE seq = ?", seq); err != nil {
			return err
		}
	}
	// Published here: none of them waits for the trigger any more.
	_, err = t.ExecContext(ctx, "DELETE FROM usage_unpublished WHERE msg = ?", id)
	return err
}

// countedUsage reads turns' counted usage (turn_usage rows matching where), oldest first.
func countedUsage(ctx context.Context, t *sql.Tx, where string, args ...any) ([]Usage, error) {
	rows, err := t.QueryContext(ctx, `SELECT turn, ts, agent, session, model, reported, input, output, cache_read, cache_write,
		thought, total, cost, currency, context_used, context_size FROM turn_usage WHERE `+where+` ORDER BY ts, turn`, args...)
	if err != nil {
		return nil, err
	}
	var all []Usage
	for rows.Next() {
		var u Usage
		var ts int64
		var cost sql.NullFloat64
		if err := rows.Scan(&u.Turn, &ts, &u.Agent, &u.Session, &u.Model, &u.Reported, &u.Input, &u.Output, &u.CacheRead,
			&u.CacheWrite, &u.Thought, &u.Total, &cost, &u.Currency, &u.ContextUsed, &u.ContextSize); err != nil {
			rows.Close()
			return nil, err
		}
		u.At = time.UnixMilli(ts).UTC()
		if cost.Valid {
			c := cost.Float64
			u.Cost = &c
		}
		all = append(all, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return all, nil
}

// pendUsageTx marks a turn's counted usage as waiting for message id to turn final
// (publishFinalized publishes it then, from the counted row).
func pendUsageTx(ctx context.Context, t *sql.Tx, id, turn string) error {
	_, err := t.ExecContext(ctx, "INSERT INTO usage_unpublished (msg, turn) VALUES (?, ?) ON CONFLICT (msg, turn) DO NOTHING", id, turn)
	return err
}

// publishFinalized publishes the waiting usage (usage_unpublished) of every message that is final
// now: each finalizer (Complete, the last Release, Cancel, Expire, ReapLeases, RequeueRunning,
// Settle) calls it in the transaction that makes messages final, so a message's counted usage is
// among its events the moment it is final, whatever ended it (a cancel between attempts, say). The
// table holds only the turns of messages under way that a retried attempt counted: nearly always
// empty: one read of it.
func (s *Store) publishFinalized(ctx context.Context, t *sql.Tx) error {
	// The waiting turns first (nearly always none), then their messages by id: a join let SQLite
	// walk every final message for the few waiting rows, milliseconds at each finalizer.
	rows, err := t.QueryContext(ctx, "SELECT DISTINCT msg FROM usage_unpublished")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return err
	}
	now := time.Now()
	for _, id := range ids {
		var seq int64
		var n int
		var state State
		err := t.QueryRowContext(ctx, "SELECT seq, events, state FROM messages WHERE id = ?", id).Scan(&seq, &n, &state)
		if errors.Is(err, sql.ErrNoRows) {
			// The message is gone (deleted): nothing waits for it any more.
			if _, err := t.ExecContext(ctx, "DELETE FROM usage_unpublished WHERE msg = ?", id); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !state.Final() {
			continue
		}
		if err := publishUsageTx(ctx, t, id, seq, n, now); err != nil {
			return err
		}
	}
	return nil
}

// usagePublishSchema keeps the usage that waits to be published as its message turns final
// (publishFinalized), and turn_usage's index by message. An earlier version did this with a
// trigger on messages: SQLite compiles a trigger's body into every UPDATE of a message's state, and
// that made Complete, Release and Cancel dear enough to nearly double the store's tests under
// -race; the trigger is dropped where it was installed.
const usagePublishSchema = `
CREATE INDEX IF NOT EXISTS turn_usage_msg ON turn_usage (msg);
CREATE TABLE IF NOT EXISTS usage_unpublished (
	msg  TEXT NOT NULL,
	turn TEXT NOT NULL,
	PRIMARY KEY (msg, turn)
) WITHOUT ROWID;
`

// migrateUsage applies usagePublishSchema and, in a store an earlier version of this branch wrote
// with its usage trigger, takes the usage that trigger was to publish over to usage_unpublished
// (the counted turns of messages not final yet that have no usage event) in the transaction that
// drops the trigger: a message cancelled after the upgrade publishes it all the same.
func (s *Store) migrateUsage() error {
	if _, err := s.w.Exec(usagePublishSchema); err != nil {
		return err
	}
	var n int
	if err := s.w.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'usage_published_when_final'").Scan(&n); err != nil || n == 0 {
		return err
	}
	return s.tx(context.Background(), func(t *sql.Tx) error {
		if _, err := t.Exec(`INSERT INTO usage_unpublished (msg, turn)
			SELECT u.msg, u.turn FROM messages m JOIN turn_usage u ON u.msg = m.id
			WHERE m.state NOT IN ('done', 'failed', 'cancelled', 'expired')
				AND NOT EXISTS (SELECT 1 FROM events e WHERE e.msg = m.seq AND e.kind = 'usage' AND json_extract(e.data, '$.turn') = u.turn)
			ON CONFLICT (msg, turn) DO NOTHING`); err != nil {
			return err
		}
		_, err := t.Exec("DROP TRIGGER usage_published_when_final")
		return err
	})
}

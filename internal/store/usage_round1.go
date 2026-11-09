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
		seq, n, err := leasedMessage(ctx, t, id, lease)
		if err != nil {
			return err
		}
		if err := putUsageTx(ctx, t, id, u, now); err != nil {
			return err
		}
		if !event {
			return nil
		}
		return publishUsageTx(ctx, t, id, seq, n, now)
	})
}

// PublishUsage puts among message id's events the usage of each of its turns that is counted and
// has no event yet (read by an attempt that was retried), from the counted row: before the message
// is final, so the app and the sender's node get it. With a lease, only while that attempt holds
// the message running (ErrLeaseLost otherwise).
func (s *Store) PublishUsage(ctx context.Context, id, lease string) error {
	now := time.Now()
	return s.tx(ctx, func(t *sql.Tx) error {
		seq, n, err := leasedMessage(ctx, t, id, lease)
		if err != nil {
			return err
		}
		return publishUsageTx(ctx, t, id, seq, n, now)
	})
}

// leasedMessage is a message's seq and event count, checked to be running under lease (when given).
func leasedMessage(ctx context.Context, t *sql.Tx, id, lease string) (int64, int, error) {
	var seq int64
	var n int
	var state State
	var cur string
	err := t.QueryRowContext(ctx, "SELECT seq, events, state, lease_id FROM messages WHERE id = ?", id).Scan(&seq, &n, &state, &cur)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, err
	}
	if lease != "" && (state != Running || cur != lease) {
		return 0, 0, ErrLeaseLost
	}
	return seq, n, nil
}

// putUsageTx counts a turn's usage, or replaces its count by a fuller reading of the same turn
// (more tokens), never by a lesser one.
func putUsageTx(ctx context.Context, t *sql.Tx, id string, u Usage, now time.Time) error {
	var had int
	err := t.QueryRowContext(ctx, "SELECT total FROM turn_usage WHERE turn = ?", u.Turn).Scan(&had)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return addUsage(ctx, t, id, u, now)
	case err != nil:
		return err
	case u.Total <= had:
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
	rows, err := t.QueryContext(ctx, `SELECT turn, ts, agent, session, model, reported, input, output, cache_read, cache_write,
		thought, total, cost, currency, context_used, context_size FROM turn_usage WHERE msg = ? ORDER BY ts, turn`, id)
	if err != nil {
		return err
	}
	var all []Usage
	for rows.Next() {
		var u Usage
		var ts int64
		var cost sql.NullFloat64
		if err := rows.Scan(&u.Turn, &ts, &u.Agent, &u.Session, &u.Model, &u.Reported, &u.Input, &u.Output, &u.CacheRead,
			&u.CacheWrite, &u.Thought, &u.Total, &cost, &u.Currency, &u.ContextUsed, &u.ContextSize); err != nil {
			rows.Close()
			return err
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
	return nil
}

// usagePublishSchema makes every move of a message to a final state (complete, the last release,
// cancel, expire, a reaped or requeued last attempt, a hub's outcome settled) publish, in the same
// write, the usage of its turns that is counted and has no event yet: an attempt that was retried
// counts what it read without an event, and whatever ends the message after it (a cancel between
// attempts, say) must not leave that usage to this node alone. The event is built from the counted
// row as PublishUsage builds it (Usage's JSON), up to maxUsageEvents usage events a message.
const usagePublishSchema = `
CREATE INDEX IF NOT EXISTS turn_usage_msg ON turn_usage (msg);
CREATE TRIGGER IF NOT EXISTS usage_published_when_final AFTER UPDATE OF state ON messages
WHEN NEW.state IN ('done', 'failed', 'cancelled', 'expired') AND OLD.state NOT IN ('done', 'failed', 'cancelled', 'expired')
BEGIN
	INSERT INTO events (msg, seq, ts, kind, data)
	SELECT NEW.seq, NEW.events + row_number() OVER (ORDER BY u.ts, u.turn),
		CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER), 'usage',
		json_object('turn', u.turn, 'at', strftime('%Y-%m-%dT%H:%M:%fZ', u.ts / 1000.0, 'unixepoch'), 'agent', u.agent,
			'session', u.session, 'model', u.model, 'reported', json(CASE WHEN u.reported THEN 'true' ELSE 'false' END),
			'input', u.input, 'output', u.output, 'cache_read', u.cache_read, 'cache_write', u.cache_write, 'thought', u.thought,
			'total', u.total, 'cost', u.cost, 'currency', u.currency, 'context_used', u.context_used, 'context_size', u.context_size)
	FROM turn_usage u
	WHERE u.msg = NEW.id
		AND NOT EXISTS (SELECT 1 FROM events e WHERE e.msg = NEW.seq AND e.kind = 'usage' AND json_extract(e.data, '$.turn') = u.turn)
		AND (SELECT count(*) FROM events e WHERE e.msg = NEW.seq AND e.kind = 'usage') < 16;
	UPDATE messages SET events = (SELECT coalesce(max(e.seq), 0) FROM events e WHERE e.msg = NEW.seq)
	WHERE seq = NEW.seq AND events < (SELECT coalesce(max(e.seq), 0) FROM events e WHERE e.msg = NEW.seq);
END;
`

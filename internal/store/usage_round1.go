package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PutUsage records a reading of a turn's usage for message id: merged into the turn's count field
// by field (a reading is what an attempt read of the turn so far: a later or fuller one carries
// more tokens, a cost line, the context; nothing already counted is lost), and marked to be
// published when the message turns final. With event, or when the message is final already, what
// the count has grown by since it was last published goes out now (publishUsageTx). With a lease,
// all of it happens only while that attempt holds the message running (ErrLeaseLost otherwise),
// in one transaction with the check.
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
		if err := putUsageTx(ctx, t, id, u, now); err != nil {
			return err
		}
		if err := pendUsageTx(ctx, t, id, u.Turn); err != nil {
			return err
		}
		if event || final {
			return publishUsageTx(ctx, t, id, seq, n, now)
		}
		return nil
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

// putUsageTx merges a reading of a turn's usage into its count: each number the larger of the two
// (both are what was read of the same turn from its start), the cost too, a model, session or
// currency where the count has none yet.
func putUsageTx(ctx context.Context, t *sql.Tx, id string, u Usage, now time.Time) error {
	had, err := countedUsage(ctx, t, "turn = ?", u.Turn)
	if err != nil {
		return err
	}
	if len(had) == 0 {
		return addUsage(ctx, t, id, u, now)
	}
	m := mergeUsage(had[0], u)
	_, err = t.ExecContext(ctx, `UPDATE turn_usage SET ts = ?, session = ?, model = ?, reported = ?, input = ?, output = ?, cache_read = ?,
		cache_write = ?, thought = ?, total = ?, cost = ?, currency = ?, context_used = ?, context_size = ? WHERE turn = ?`,
		usageAt(m.At, now), m.Session, m.Model, m.Reported, m.Input, m.Output, m.CacheRead, m.CacheWrite, m.Thought, m.Total, m.Cost,
		m.Currency, m.ContextUsed, m.ContextSize, m.Turn)
	return err
}

// mergeUsage is two readings of one turn as one: the larger of each number, what either says.
func mergeUsage(a, b Usage) Usage {
	m := a
	m.Input, m.Output, m.CacheRead, m.CacheWrite = max(a.Input, b.Input), max(a.Output, b.Output), max(a.CacheRead, b.CacheRead), max(a.CacheWrite, b.CacheWrite)
	m.Thought, m.Total, m.ContextUsed, m.ContextSize = max(a.Thought, b.Thought), max(a.Total, b.Total), max(a.ContextUsed, b.ContextUsed), max(a.ContextSize, b.ContextSize)
	m.Reported = a.Reported || b.Reported
	if b.Cost != nil && (a.Cost == nil || *b.Cost > *a.Cost) {
		c := *b.Cost
		m.Cost = &c
	}
	m.Model, m.Session, m.Currency = cmpOr(a.Model, b.Model), cmpOr(a.Session, b.Session), cmpOr(a.Currency, b.Currency)
	if b.At.After(a.At) {
		m.At = b.At
	}
	return m
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// published is what of a turn's count its events carry: the sum of them, and how many there are.
type published struct {
	Usage
	events int
}

// publishedOf is what of turn has been published among message seq's events: kept in
// usage_published, or (a turn an earlier version published) its one event.
func publishedOf(ctx context.Context, t *sql.Tx, seq int64, turn string) (*published, error) {
	var p published
	var cost sql.NullFloat64
	err := t.QueryRowContext(ctx, `SELECT events, input, output, cache_read, cache_write, thought, total, cost, context_used, context_size
		FROM usage_published WHERE turn = ?`, turn).Scan(&p.events, &p.Input, &p.Output, &p.CacheRead, &p.CacheWrite, &p.Thought, &p.Total, &cost,
		&p.ContextUsed, &p.ContextSize)
	if err == nil {
		if cost.Valid {
			c := cost.Float64
			p.Cost = &c
		}
		return &p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var data string
	err = t.QueryRowContext(ctx, "SELECT data FROM events WHERE msg = ? AND kind = 'usage' AND json_extract(data, '$.turn') = ? LIMIT 1", seq, turn).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u, _ := readUsage(data)
	return &published{Usage: u, events: 1}, nil
}

// publishUsageTx publishes the waiting turns of message id (usage_unpublished): a turn not
// published yet as one "usage" event of its count; a turn published before (the message reopened,
// a retry read more of it) as a further event of what the count grew by, its id the turn's and
// the event's number (turn#2, turn#3, …). An event once out is never changed: every node summing a
// message's usage events has the count this node has. Up to maxUsageEvents usage events a message.
func publishUsageTx(ctx context.Context, t *sql.Tx, id string, seq int64, n int, now time.Time) error {
	rows, err := t.QueryContext(ctx, "SELECT turn FROM usage_unpublished WHERE msg = ? ORDER BY turn", id)
	if err != nil {
		return err
	}
	var turns []string
	for rows.Next() {
		var turn string
		if err := rows.Scan(&turn); err != nil {
			rows.Close()
			return err
		}
		turns = append(turns, turn)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, turn := range turns {
		got, err := countedUsage(ctx, t, "turn = ?", turn)
		if err != nil {
			return err
		}
		if len(got) == 0 {
			continue
		}
		c := got[0]
		p, err := publishedOf(ctx, t, seq, turn)
		if err != nil {
			return err
		}
		ev, k := c, 1
		if p != nil {
			ev, k = grownBy(c, p.Usage), p.events+1
			if ev.Total == 0 && ev.Input+ev.Output+ev.CacheRead+ev.CacheWrite+ev.Thought == 0 && ev.Cost == nil {
				continue // nothing more than what is out
			}
			ev.Turn = fmt.Sprintf("%s#%d", turn, k)
		}
		var have int
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE msg = ? AND kind = 'usage'", seq).Scan(&have); err != nil {
			return err
		}
		if have >= maxUsageEvents {
			break
		}
		b, err := json.Marshal(ev)
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
		if _, err := t.ExecContext(ctx, `INSERT INTO usage_published (turn, events, input, output, cache_read, cache_write, thought, total, cost,
			context_used, context_size) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (turn) DO UPDATE SET events = excluded.events,
			input = excluded.input, output = excluded.output, cache_read = excluded.cache_read, cache_write = excluded.cache_write,
			thought = excluded.thought, total = excluded.total, cost = excluded.cost, context_used = excluded.context_used,
			context_size = excluded.context_size`, turn, k, c.Input, c.Output, c.CacheRead, c.CacheWrite, c.Thought, c.Total, c.Cost,
			c.ContextUsed, c.ContextSize); err != nil {
			return err
		}
	}
	// Published here: none of them waits for a finalizer any more.
	_, err = t.ExecContext(ctx, "DELETE FROM usage_unpublished WHERE msg = ?", id)
	return err
}

// grownBy is what count c has more than what was published (p): each number's difference, the cost's
// too; the context as c has it (a level, not a sum).
func grownBy(c, p Usage) Usage {
	d := c
	d.Input, d.Output, d.CacheRead, d.CacheWrite = max(c.Input-p.Input, 0), max(c.Output-p.Output, 0), max(c.CacheRead-p.CacheRead, 0), max(c.CacheWrite-p.CacheWrite, 0)
	d.Thought, d.Total = max(c.Thought-p.Thought, 0), max(c.Total-p.Total, 0)
	d.Cost = nil
	if c.Cost != nil {
		was := 0.0
		if p.Cost != nil {
			was = *p.Cost
		}
		if *c.Cost > was {
			x := *c.Cost - was
			d.Cost = &x
		}
	}
	return d
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
-- What of a turn's count its message's usage events carry (their sum, and how many): a turn that
-- grows after it was published goes out again as what it grew by (publishUsageTx).
CREATE TABLE IF NOT EXISTS usage_published (
	turn         TEXT    PRIMARY KEY,
	events       INTEGER NOT NULL,
	input        INTEGER NOT NULL DEFAULT 0,
	output       INTEGER NOT NULL DEFAULT 0,
	cache_read   INTEGER NOT NULL DEFAULT 0,
	cache_write  INTEGER NOT NULL DEFAULT 0,
	thought      INTEGER NOT NULL DEFAULT 0,
	total        INTEGER NOT NULL DEFAULT 0,
	cost         REAL,
	context_used INTEGER NOT NULL DEFAULT 0,
	context_size INTEGER NOT NULL DEFAULT 0
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

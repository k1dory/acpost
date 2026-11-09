package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// What agents' turns cost: one row per turn, written with the turn's "usage" event (where the agent
// runs, and wherever its events are copied: the hub, the sender's node). Rows outlive messages
// (UsageRetention) so periods longer than the messages' retention can be counted.

// UsageRetention is how long a turn's usage is kept.
const UsageRetention = 400 * 24 * time.Hour

// maxUsageEvents bounds the "usage" events one message may have (one a turn: attempts): they are
// kept past MaxEventsPerMessage, so the turns' counts reach every node, but not without end.
const maxUsageEvents = 16

// Usage is a turn's usage as the agent reported it, recorded where the agent runs.
type Usage struct {
	Turn    string    `json:"turn"` // the turn's own id: counted once wherever its event goes
	At      time.Time `json:"at"`   // when the turn ended
	Agent   string    `json:"agent"`
	Session string    `json:"session,omitempty"`
	Model   string    `json:"model,omitempty"`
	// Reported says the agent gave the turn's tokens (an optional, unstable part of ACP).
	Reported   bool `json:"reported"`
	Input      int  `json:"input,omitempty"` // without the cache
	Output     int  `json:"output,omitempty"`
	CacheRead  int  `json:"cache_read,omitempty"`
	CacheWrite int  `json:"cache_write,omitempty"`
	Thought    int  `json:"thought,omitempty"`
	Total      int  `json:"total,omitempty"`
	// Cost is what the turn cost by the agent's account (its session's cost grew by it), when the
	// agent says.
	Cost        *float64 `json:"cost,omitempty"`
	Currency    string   `json:"currency,omitempty"`
	ContextUsed int      `json:"context_used,omitempty"`
	ContextSize int      `json:"context_size,omitempty"`
}

const usageSchema = `
CREATE TABLE IF NOT EXISTS turn_usage (
	turn         TEXT    PRIMARY KEY,
	msg          TEXT    NOT NULL,
	ts           INTEGER NOT NULL,
	agent        TEXT    NOT NULL,
	session      TEXT    NOT NULL DEFAULT '',
	model        TEXT    NOT NULL DEFAULT '',
	reported     INTEGER NOT NULL DEFAULT 0,
	input        INTEGER NOT NULL DEFAULT 0,
	output       INTEGER NOT NULL DEFAULT 0,
	cache_read   INTEGER NOT NULL DEFAULT 0,
	cache_write  INTEGER NOT NULL DEFAULT 0,
	thought      INTEGER NOT NULL DEFAULT 0,
	total        INTEGER NOT NULL DEFAULT 0,
	cost         REAL,
	currency     TEXT    NOT NULL DEFAULT '',
	context_used INTEGER NOT NULL DEFAULT 0,
	context_size INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS turn_usage_ts ON turn_usage (ts);
-- A session's cumulative cost as its agent last said it, where the agent runs: a turn's cost is
-- what it grew by.
CREATE TABLE IF NOT EXISTS session_costs (
	agent    TEXT NOT NULL,
	session  TEXT NOT NULL,
	currency TEXT NOT NULL,
	cost     REAL NOT NULL,
	ts       INTEGER NOT NULL,
	PRIMARY KEY (agent, session, currency)
) WITHOUT ROWID;
`

// TurnCost is what a turn of a session cost, from the session's cumulative cost after it: how much
// that grew since the session's turn before (a reset starts it over). For a session's first turn
// here the growth is from base, when it is known (0 for a session this node created; what the
// agent said when the session was loaded); otherwise it is not known (ok false), and the turn
// starts the count.
func (s *Store) TurnCost(ctx context.Context, agent, session, currency string, cumulative float64, base *float64) (cost float64, ok bool, err error) {
	err = s.tx(ctx, func(t *sql.Tx) error {
		var prev float64
		err := t.QueryRowContext(ctx, "SELECT cost FROM session_costs WHERE agent = ? AND session = ? AND currency = ?", agent, session, currency).Scan(&prev)
		switch {
		case err == nil:
			ok = true
		case errors.Is(err, sql.ErrNoRows) && base != nil:
			prev, ok = *base, true
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		cost = max(cumulative-prev, 0)
		if cumulative < prev {
			cost = cumulative
		}
		_, err = t.ExecContext(ctx, `INSERT INTO session_costs (agent, session, currency, cost, ts) VALUES (?,?,?,?,?)
			ON CONFLICT (agent, session, currency) DO UPDATE SET cost = excluded.cost, ts = excluded.ts`, agent, session, currency, cumulative, ms(time.Now()))
		return err
	})
	return cost, ok, err
}

// usageAt is when a turn is counted: its own time, unless that is not believable.
func usageAt(at, now time.Time) int64 {
	if at.IsZero() || at.After(now.Add(time.Hour)) || at.Before(now.Add(-UsageRetention)) {
		return ms(now)
	}
	return ms(at)
}

// readUsage reads a "usage" event's data; false when it is not a turn's usage.
func readUsage(data string) (Usage, bool) {
	var u Usage
	if json.Unmarshal([]byte(data), &u) != nil || u.Turn == "" || u.Agent == "" {
		return u, false
	}
	return u, true
}

// addUsage counts a turn once: a turn already counted (its event come again by another way) is not.
func addUsage(ctx context.Context, t *sql.Tx, msg string, u Usage, now time.Time) error {
	_, err := t.ExecContext(ctx, `INSERT INTO turn_usage (turn, msg, ts, agent, session, model, reported, input, output,
		cache_read, cache_write, thought, total, cost, currency, context_used, context_size) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (turn) DO NOTHING`,
		u.Turn, msg, usageAt(u.At, now), u.Agent, u.Session, u.Model, u.Reported, u.Input, u.Output, u.CacheRead, u.CacheWrite,
		u.Thought, u.Total, u.Cost, u.Currency, u.ContextUsed, u.ContextSize)
	return err
}

// UsageQuery selects and groups turns' usage.
type UsageQuery struct {
	From, To time.Time // [From, To); zero: unbounded
	By       string    // "", agent, model, day, session
	Agent    string    // only this agent (agent:name@node)
}

// UsageSum is the usage of a group of turns.
type UsageSum struct {
	Key        string `json:"key,omitempty"`
	Turns      int    `json:"turns"`
	Reported   int    `json:"reported"` // turns whose tokens the agent gave
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read"`
	CacheWrite int64  `json:"cache_write"`
	Thought    int64  `json:"thought"`
	Total      int64  `json:"total"`
	// Cost is what the turns cost by the agents' own account, in Currency; Costed turns had it.
	Cost     float64   `json:"cost"`
	Costed   int       `json:"costed"`
	Currency string    `json:"currency,omitempty"` // "mixed" when the turns gave several
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
	// Uncosted is, per model, the tokens of turns without a cost of their own: what a price list
	// kept outside can estimate.
	Uncosted map[string]ModelTokens `json:"uncosted,omitempty"`
}

// ModelTokens are the tokens of one model within a group.
type ModelTokens struct {
	Turns      int   `json:"turns"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

var usageKeys = map[string]string{
	"":        "''",
	"agent":   "agent",
	"model":   "model",
	"session": "agent || ' ' || session",
	"day":     "strftime('%Y-%m-%d', ts / 1000, 'unixepoch', 'localtime')",
}

// UsageOf sums turns' usage, grouped as q says, in key order.
func (s *Store) UsageOf(ctx context.Context, q UsageQuery) ([]UsageSum, error) {
	key, ok := usageKeys[q.By]
	if !ok {
		return nil, fmt.Errorf("usage by %q: by agent, model, day or session", q.By)
	}
	from, to := int64(0), int64(1<<62)
	if !q.From.IsZero() {
		from = ms(q.From)
	}
	if !q.To.IsZero() {
		to = ms(q.To)
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT `+key+` AS k, model, currency, count(*), sum(reported), sum(input), sum(output), sum(cache_read),
			sum(cache_write), sum(thought), sum(total), coalesce(sum(cost), 0), count(cost), min(ts), max(ts)
		FROM turn_usage WHERE ts >= ? AND ts < ? AND (agent = ? OR ? = '')
		GROUP BY k, model, currency, cost IS NULL ORDER BY k`, from, to, q.Agent, q.Agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageSum
	for rows.Next() {
		var k, model, cur string
		var turns, reported, costed int
		var in, outT, cr, cw, th, tot, first, last int64
		var cost float64
		if err := rows.Scan(&k, &model, &cur, &turns, &reported, &in, &outT, &cr, &cw, &th, &tot, &cost, &costed, &first, &last); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Key != k {
			out = append(out, UsageSum{Key: k, First: fromMS(first), Uncosted: map[string]ModelTokens{}})
		}
		g := &out[len(out)-1]
		g.Turns += turns
		g.Reported += reported
		g.Input, g.Output, g.CacheRead, g.CacheWrite, g.Thought, g.Total = g.Input+in, g.Output+outT, g.CacheRead+cr, g.CacheWrite+cw, g.Thought+th, g.Total+tot
		if costed > 0 {
			g.Cost += cost
			g.Costed += costed
			switch {
			case g.Currency == "":
				g.Currency = cur
			case g.Currency != cur:
				g.Currency = "mixed"
			}
		}
		if t := fromMS(first); t.Before(g.First) {
			g.First = t
		}
		if t := fromMS(last); t.After(g.Last) {
			g.Last = t
		}
		if costed == 0 {
			m := g.Uncosted[model]
			m.Turns += turns
			m.Input, m.Output, m.CacheRead, m.CacheWrite = m.Input+in, m.Output+outT, m.CacheRead+cr, m.CacheWrite+cw
			g.Uncosted[model] = m
		}
	}
	return out, rows.Err()
}

// gcUsage drops usage older than UsageRetention, and the costs of sessions not heard of since.
func gcUsage(ctx context.Context, t *sql.Tx, now time.Time) error {
	before := ms(now.Add(-UsageRetention))
	if _, err := t.ExecContext(ctx, "DELETE FROM turn_usage WHERE ts < ?", before); err != nil {
		return err
	}
	// What was published of the turns gone goes with them (usage_round1.go).
	if _, err := t.ExecContext(ctx, "DELETE FROM usage_published WHERE turn NOT IN (SELECT turn FROM turn_usage)"); err != nil {
		return err
	}
	_, err := t.ExecContext(ctx, "DELETE FROM session_costs WHERE ts < ?", before)
	return err
}

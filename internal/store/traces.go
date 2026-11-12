package store

import (
	"context"
	"encoding/json"
)

// What a trace of agents' time is read from: the threads people started, and a thread's messages
// with those its agents sent while they worked (subagents).

// TraceThreads are the threads a person started (one of its messages is not under another), the
// most recently active first, at most limit.
func (s *Store) TraceThreads(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	rows, err := s.r.QueryContext(ctx, `SELECT thread FROM messages GROUP BY thread HAVING min(depth) = 0
		ORDER BY max(updated_at) DESC, thread DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TraceMessages are a thread's last own messages and, at most under more, the messages sent while
// they ran (and while those ran), without bodies, in the order they were sent. cut says that older
// own messages, or more of those under them, were left out.
func (s *Store) TraceMessages(ctx context.Context, thread string, own, under int) ([]Message, bool, error) {
	if own <= 0 || own > MaxPage {
		own = MaxPage
	}
	under = max(under, 0)
	ids, err := s.strings(ctx, `SELECT id FROM messages WHERE thread = ? ORDER BY seq DESC LIMIT ?`, thread, own+1)
	if err != nil {
		return nil, false, err
	}
	cut := len(ids) > own
	ids = ids[:min(len(ids), own)]
	if len(ids) == 0 {
		return nil, false, nil
	}
	ownJSON, _ := json.Marshal(ids)
	// One more than asked for says there were more. The walk itself is bounded too: a message of
	// the thread under another of it is met twice (as its own and under), at most once each.
	under1, err := s.strings(ctx, `WITH RECURSIVE
			own(id) AS (SELECT value FROM json_each(?)),
			d(id, depth) AS (
				SELECT id, 0 FROM own
				UNION SELECT m.id, d.depth + 1 FROM messages m JOIN d ON m.parent = d.id WHERE d.depth < 16
				LIMIT ?
			)
		SELECT DISTINCT id FROM d WHERE id NOT IN (SELECT id FROM own) LIMIT ?`, string(ownJSON), 2*len(ids)+2*(under+1), under+1)
	if err != nil {
		return nil, false, err
	}
	if len(under1) > under {
		cut = true
		under1 = under1[:under]
	}
	all, _ := json.Marshal(append(ids, under1...))
	rows, err := s.r.QueryContext(ctx, `SELECT `+cols+` FROM messages m WHERE m.id IN (SELECT value FROM json_each(?)) ORDER BY m.seq`, string(all))
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		m.LeaseID = ""
		out = append(out, m)
	}
	return out, cut, rows.Err()
}

// strings is a query's one text column.
func (s *Store) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

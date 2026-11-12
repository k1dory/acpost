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
	// The messages under them, level by level, each once: a message has one parent, so it is met
	// once, unless it is one of the thread's own (a turn of the thread asked by another of it).
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	var under1 []string
	over := false
	level := ids
	for depth := 0; len(level) > 0 && !over; depth++ {
		if depth == 16 {
			over = true // deeper than a chain of messages goes (limits.max_depth)
			break
		}
		lj, _ := json.Marshal(level)
		kids, err := s.strings(ctx, `SELECT id FROM messages WHERE parent IN (SELECT value FROM json_each(?)) ORDER BY seq LIMIT ?`,
			string(lj), under-len(under1)+1+len(ids))
		if err != nil {
			return nil, false, err
		}
		level = level[:0:0]
		for _, k := range kids {
			if seen[k] {
				continue
			}
			if len(under1) == under {
				over = true // one more than asked for
				break
			}
			seen[k] = true
			under1 = append(under1, k)
			level = append(level, k)
		}
	}
	cut = cut || over
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

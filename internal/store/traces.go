package store

import "context"

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
// they ran (and while those ran), without bodies, in the order they were sent.
func (s *Store) TraceMessages(ctx context.Context, thread string, own, under int) ([]Message, error) {
	if own <= 0 || own > MaxPage {
		own = MaxPage
	}
	under = max(under, 0)
	rows, err := s.r.QueryContext(ctx, `WITH RECURSIVE
			own(seq) AS (SELECT seq FROM messages WHERE thread = ? ORDER BY seq DESC LIMIT ?),
			d(id, depth) AS (
				SELECT m.id, 0 FROM messages m JOIN own ON own.seq = m.seq
				UNION SELECT m.id, d.depth + 1 FROM messages m JOIN d ON m.parent = d.id WHERE d.depth < 16
			)
		SELECT `+cols+` FROM messages m WHERE m.id IN (SELECT id FROM d LIMIT ?) ORDER BY m.seq`, thread, own, own+under)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		m.LeaseID = ""
		out = append(out, m)
	}
	return out, rows.Err()
}

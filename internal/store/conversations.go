package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Conversation is a thread: a message and those that went on from it, as a conversation list
// shows it.
type Conversation struct {
	Thread   string    `json:"thread"` // the first message's id
	Task     string    `json:"task,omitempty"`
	Title    string    `json:"title"`  // the start of the first message
	Agents   []string  `json:"agents"` // who was asked
	Messages int       `json:"messages"`
	Running  int       `json:"running"`
	Queued   int       `json:"queued"`
	Failed   int       `json:"failed"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
}

// Conversations lists threads, the most recently active first, a page at a time: before is the
// cursor of the page before ("" for the first), and the cursor of the next comes back ("" at the
// end). party, when given, keeps what it may see.
func (s *Store) Conversations(ctx context.Context, party *Party, before string, limit int) ([]Conversation, string, error) {
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	var where []string
	var args []any
	if party != nil {
		cond, pargs := party.where("")
		where = append(where, cond)
		args = append(args, pargs...)
	}
	q := `SELECT thread, max(updated_at) AS upd, min(created_at), count(*), sum(state = 'running'), sum(state = 'queued'),
		sum(state IN ('failed', 'expired')), group_concat(DISTINCT recipient) FROM messages`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " GROUP BY thread"
	if before != "" {
		ts, thread, _ := strings.Cut(before, "/")
		at, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return nil, "", errors.New("a conversations cursor is a time/thread")
		}
		q += " HAVING upd < ? OR (upd = ? AND thread < ?)"
		args = append(args, at, at, thread)
	}
	q += " ORDER BY upd DESC, thread DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	var out []Conversation
	var upds []int64
	for rows.Next() {
		var c Conversation
		var upd, started int64
		var agents string
		if err := rows.Scan(&c.Thread, &upd, &started, &c.Messages, &c.Running, &c.Queued, &c.Failed, &agents); err != nil {
			rows.Close()
			return nil, "", err
		}
		c.Updated, c.Started = fromMS(upd), fromMS(started)
		c.Agents = strings.Split(agents, ",")
		out = append(out, c)
		upds = append(upds, upd)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out, upds = out[:limit], upds[:limit]
		next = strconv.FormatInt(upds[limit-1], 10) + "/" + out[limit-1].Thread
	}
	// A title: the start of the thread's first message the party may see (all of them may: its
	// root), and its task.
	for i := range out {
		tq := `SELECT m.task, substr(c.body, 1, 200) FROM messages m JOIN contents c ON c.seq = m.seq WHERE m.thread = ?`
		targs := []any{out[i].Thread}
		if party != nil {
			cond, pargs := party.where("m.")
			tq += " AND " + cond
			targs = append(targs, pargs...)
		}
		tq += " ORDER BY m.seq LIMIT 1"
		_ = s.r.QueryRowContext(ctx, tq, targs...).Scan(&out[i].Task, &out[i].Title)
		out[i].Title = firstLine(out[i].Title)
	}
	return out, next, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.ToValidUTF8(s, "")
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The backlog: tasks the node's user wrote down to give an agent later (the board's first column).

const backlogSchema = `
CREATE TABLE IF NOT EXISTS backlog (
	id      TEXT    PRIMARY KEY,
	title   TEXT    NOT NULL,
	body    TEXT    NOT NULL DEFAULT '',
	agent   TEXT    NOT NULL DEFAULT '',
	cwd     TEXT    NOT NULL DEFAULT '',
	pos     INTEGER NOT NULL DEFAULT 0,
	created INTEGER NOT NULL
) WITHOUT ROWID;
`

// BacklogItem is a task kept for later.
type BacklogItem struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Body    string    `json:"body,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	Created time.Time `json:"created"`
}

// ErrNoBacklogItem: no such item.
var ErrNoBacklogItem = errors.New("no such backlog item")

func (s *Store) Backlog(ctx context.Context) ([]BacklogItem, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, title, body, agent, cwd, created FROM backlog ORDER BY pos, created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BacklogItem{}
	for rows.Next() {
		var b BacklogItem
		var c int64
		if err := rows.Scan(&b.ID, &b.Title, &b.Body, &b.Agent, &b.Cwd, &c); err != nil {
			return nil, err
		}
		b.Created = fromMS(c)
		out = append(out, b)
	}
	return out, rows.Err()
}

// PutBacklogItem adds (a new id) or changes an item.
func (s *Store) PutBacklogItem(ctx context.Context, b BacklogItem) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO backlog (id, title, body, agent, cwd, created) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET title = excluded.title, body = excluded.body, agent = excluded.agent, cwd = excluded.cwd`,
		b.ID, b.Title, b.Body, b.Agent, b.Cwd, ms(time.Now()))
	return err
}

// BacklogItemOf is one item.
func (s *Store) BacklogItemOf(ctx context.Context, id string) (BacklogItem, error) {
	var b BacklogItem
	var c int64
	err := s.r.QueryRowContext(ctx, `SELECT id, title, body, agent, cwd, created FROM backlog WHERE id = ?`, id).
		Scan(&b.ID, &b.Title, &b.Body, &b.Agent, &b.Cwd, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNoBacklogItem
	}
	b.Created = fromMS(c)
	return b, err
}

// DeleteBacklogItem removes one.
func (s *Store) DeleteBacklogItem(ctx context.Context, id string) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM backlog WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNoBacklogItem
	}
	return nil
}

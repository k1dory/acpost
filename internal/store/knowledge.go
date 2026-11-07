package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Knowledge: notes the node's user writes once (how the project builds, what not to touch) that an
// agent is given at the start of each new session, everywhere or under a directory.

const knowledgeSchema = `
CREATE TABLE IF NOT EXISTS knowledge (
	name    TEXT    PRIMARY KEY,
	body    TEXT    NOT NULL,
	scope   TEXT    NOT NULL DEFAULT '', -- a directory: sessions under it; empty: every session
	enabled INTEGER NOT NULL DEFAULT 1,
	ts      INTEGER NOT NULL
) WITHOUT ROWID;
`

// Note is a piece of knowledge.
type Note struct {
	Name    string    `json:"name"`
	Body    string    `json:"body"`
	Scope   string    `json:"scope,omitempty"`
	Enabled bool      `json:"enabled"`
	Updated time.Time `json:"updated"`
}

// ErrNoNote: no such note.
var ErrNoNote = errors.New("no such note")

// MaxNote bounds a note's text; MaxKnowledge what a session is given of them all.
const (
	MaxNote      = 16 << 10
	MaxKnowledge = 32 << 10
)

func (s *Store) Knowledge(ctx context.Context) ([]Note, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT name, body, scope, enabled, ts FROM knowledge ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var k Note
		var ts int64
		if err := rows.Scan(&k.Name, &k.Body, &k.Scope, &k.Enabled, &ts); err != nil {
			return nil, err
		}
		k.Updated = time.UnixMilli(ts).UTC()
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) PutNote(ctx context.Context, k Note) error {
	k.Name = strings.TrimSpace(k.Name)
	if k.Name == "" || len(k.Name) > 120 {
		return fmt.Errorf("a note's name: 1 to 120 characters")
	}
	if strings.TrimSpace(k.Body) == "" || len(k.Body) > MaxNote {
		return fmt.Errorf("a note's text: 1 byte to %d KiB", MaxNote>>10)
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO knowledge (name, body, scope, enabled, ts) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET body = excluded.body, scope = excluded.scope, enabled = excluded.enabled, ts = excluded.ts`,
		k.Name, k.Body, k.Scope, k.Enabled, time.Now().UnixMilli())
	return err
}

func (s *Store) DeleteNote(ctx context.Context, name string) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM knowledge WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNoNote
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// What a turn changed in the git repository its agent works in: the working tree before and after
// it (snapshot trees in the repository's own object store), the files, and the diff as text (kept
// here: the trees' objects go when the repository is collected). They go with their message.

// ChangedFile is a file a turn changed.
type ChangedFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"`         // A, M, D, R…
	From    string `json:"from,omitempty"` // a rename's or copy's source path (rows recorded before it have none)
	Added   int    `json:"added"`          // lines; -1 for a binary file
	Removed int    `json:"removed"`
}

// TurnChanges is what a turn changed.
type TurnChanges struct {
	Msg    string        `json:"msg"`
	Agent  string        `json:"agent"`
	Start  time.Time     `json:"start"` // when the turn's working tree was taken before it
	At     time.Time     `json:"at"`    // and after it
	Repo   string        `json:"repo"`
	Branch string        `json:"branch,omitempty"`
	Before string        `json:"before"` // tree ids
	After  string        `json:"after"`
	Files  []ChangedFile `json:"files"`
	Diff   string        `json:"diff,omitempty"`
	Cut    bool          `json:"cut,omitempty"` // the diff was longer than kept
}

// RepoDiff is what changed in a repository over several turns.
type RepoDiff struct {
	Repo   string        `json:"repo"`
	Branch string        `json:"branch,omitempty"`
	Before string        `json:"before"`
	After  string        `json:"after"`
	Files  []ChangedFile `json:"files"`
	Diff   string        `json:"diff,omitempty"`
	Cut    bool          `json:"cut,omitempty"`
}

const changesSchema = `
CREATE TABLE IF NOT EXISTS turn_changes (
	id     INTEGER PRIMARY KEY,
	msg    TEXT    NOT NULL,
	ts     INTEGER NOT NULL,
	start  INTEGER NOT NULL,
	agent  TEXT    NOT NULL,
	repo   TEXT    NOT NULL,
	branch TEXT    NOT NULL DEFAULT '',
	before TEXT    NOT NULL,
	after  TEXT    NOT NULL,
	files  TEXT    NOT NULL,
	diff   TEXT    NOT NULL,
	cut    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS turn_changes_msg ON turn_changes (msg);
CREATE INDEX IF NOT EXISTS messages_parent ON messages (parent) WHERE parent != '';
`

// AddChanges records what a turn of a message changed.
func (s *Store) AddChanges(ctx context.Context, c TurnChanges) error {
	files, _ := json.Marshal(c.Files)
	return s.tx(ctx, func(t *sql.Tx) error {
		_, err := t.ExecContext(ctx, `INSERT INTO turn_changes (msg, ts, start, agent, repo, branch, before, after, files, diff, cut)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`, c.Msg, ms(c.At), ms(c.Start), c.Agent, c.Repo, c.Branch, c.Before, c.After, string(files), c.Diff, c.Cut)
		return err
	})
}

// ChangesOf is what the turns of these messages changed, oldest first; withDiff reads the diffs.
func (s *Store) ChangesOf(ctx context.Context, ids []string, withDiff bool) ([]TurnChanges, error) {
	var out []TurnChanges
	for _, id := range ids {
		diff := "''"
		if withDiff {
			diff = "diff"
		}
		rows, err := s.r.QueryContext(ctx, `SELECT msg, ts, start, agent, repo, branch, before, after, files, `+diff+`, cut
			FROM turn_changes WHERE msg = ? ORDER BY ts, id`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c TurnChanges
			var ts, start int64
			var files string
			if err := rows.Scan(&c.Msg, &ts, &start, &c.Agent, &c.Repo, &c.Branch, &c.Before, &c.After, &files, &c.Diff, &c.Cut); err != nil {
				rows.Close()
				return nil, err
			}
			c.At, c.Start = fromMS(ts), fromMS(start)
			_ = json.Unmarshal([]byte(files), &c.Files)
			out = append(out, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	sortChanges(out)
	return out, nil
}

func sortChanges(cs []TurnChanges) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].At.Before(cs[j-1].At); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

// Descendants is a message and the messages sent while it ran, theirs too, at most limit: the work
// of the agents it asked (subagents).
func (s *Store) Descendants(ctx context.Context, id string, limit int) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `WITH RECURSIVE d(id, depth) AS (
			SELECT ?, 0
			UNION SELECT m.id, d.depth + 1 FROM messages m JOIN d ON m.parent = d.id WHERE d.depth < 16
		) SELECT id FROM d LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ChangedRepo says whether a turn's snapshot recorded tree (before or after it) in repo.
func (s *Store) ChangedRepo(ctx context.Context, repo, tree string) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM turn_changes WHERE repo = ? AND (before = ? OR after = ?)`, repo, tree, tree).Scan(&n)
	return n > 0, err
}

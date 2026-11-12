package store

// The library's documents on disk, kept as a projection of the turns' changes: one row per path of
// a document type (or JSON), as the latest change left it, updated in the transaction that
// records a turn's changes. A listing is then an indexed read, never a replay of the history.
//
// A row says whether a turn made the file there now (created it, or renamed or copied there one it
// made): a later edit keeps that, a deletion or a rename away ends it. Only paths of the types
// the library lists are kept, so a rename carries "made" from such a path only (a temporary
// report.tmp renamed to report.json is not counted as made). Retention removes a row with the
// message of its latest change; collecting older changes leaves it as it is.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"

	"modernc.org/sqlite"
)

const artifactDocsSchema = `
CREATE TABLE IF NOT EXISTS artifact_docs (
	path   TEXT PRIMARY KEY,
	ts     INTEGER NOT NULL,
	change INTEGER NOT NULL,
	idx    INTEGER NOT NULL,
	msg    TEXT NOT NULL,
	thread TEXT NOT NULL DEFAULT '',
	agent  TEXT NOT NULL,
	status TEXT NOT NULL,
	made   INTEGER NOT NULL DEFAULT 0,
	listed INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS artifact_docs_listed ON artifact_docs (ts, change, idx) WHERE listed = 1;
CREATE INDEX IF NOT EXISTS artifact_docs_msg ON artifact_docs (msg);
`

// fold is the one rule a filter matches by, for every kind of artifact: Unicode lower case, in
// Go and in SQL (acpost_fold), so "отчёт" finds "Отчёт.md" wherever it is kept.
func fold(s string) string { return strings.ToLower(s) }

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("acpost_fold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return fold(v), nil
		case []byte:
			return fold(string(v)), nil
		case nil:
			return "", nil
		}
		return nil, errors.New("acpost_fold: text")
	})
}

// docKept says whether the projection keeps a path: a type the library lists, or JSON.
func docKept(p string) bool {
	low := strings.ToLower(p)
	for _, e := range docExts {
		if strings.HasSuffix(low, e) {
			return true
		}
	}
	return strings.HasSuffix(low, ".json")
}

// docRow is a path as the projection has it.
type docRow struct {
	key    artifactKey
	status string
	made   bool
}

func getDoc(ctx context.Context, t *sql.Tx, path string) (*docRow, error) {
	var d docRow
	err := t.QueryRowContext(ctx, "SELECT ts, change, idx, status, made FROM artifact_docs WHERE path = ?", path).
		Scan(&d.key.ts, &d.key.id, &d.key.idx, &d.status, &d.made)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &d, err
}

// isNow says whether a row holds a file there now, made by a turn.
func (d *docRow) madeNow() bool { return d != nil && d.made && !strings.HasPrefix(d.status, "D") }

// applyChanges updates the projection with one turn's changes, in their order. A change older
// than what a path's row has (turns recorded out of order) leaves that row.
func applyChanges(ctx context.Context, t *sql.Tx, c TurnChanges, change int64, thread string) error {
	put := func(path string, k artifactKey, status string, made bool, have *docRow) error {
		if have != nil && !have.key.less(k) {
			return nil
		}
		listed := isDoc(path, status, made)
		_, err := t.ExecContext(ctx, `INSERT INTO artifact_docs (path, ts, change, idx, msg, thread, agent, status, made, listed)
			VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT (path) DO UPDATE SET ts = excluded.ts, change = excluded.change, idx = excluded.idx,
			msg = excluded.msg, thread = excluded.thread, agent = excluded.agent, status = excluded.status, made = excluded.made, listed = excluded.listed`,
			path, k.ts, k.id, k.idx, c.Msg, thread, c.Agent, status, made, listed)
		return err
	}
	for i, f := range c.Files {
		if f.Path == "" {
			continue
		}
		rename := (strings.HasPrefix(f.Status, "R") || strings.HasPrefix(f.Status, "C")) && f.From != ""
		if !docKept(f.Path) && !rename {
			continue // a source file: nothing of it is kept, nothing to read
		}
		k := artifactKey{ts: ms(c.At), id: change, idx: int64(i)}
		w := wholePath(c.Repo, f.Path)
		cur, err := getDoc(ctx, t, w)
		if err != nil {
			return err
		}
		made := cur.madeNow()
		switch {
		case f.Status == "A":
			made = true
		case strings.HasPrefix(f.Status, "D"):
			made = false
		case rename:
			sw := wholePath(c.Repo, f.From)
			src, err := getDoc(ctx, t, sw)
			if err != nil {
				return err
			}
			made = src.madeNow()
			if strings.HasPrefix(f.Status, "R") && src != nil { // a rename retires its source
				if err := put(sw, k, "D", false, src); err != nil {
					return err
				}
			}
		}
		if docKept(f.Path) {
			if err := put(w, k, f.Status, made, cur); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildArtifactDocs fills the projection from the changes a store already has, once (a store from
// before it): replayed in order, a batch at a time.
func (s *Store) buildArtifactDocs() error {
	var have int
	if err := s.w.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'artifact_docs'").Scan(&have); err != nil || have > 0 {
		return err
	}
	if _, err := s.w.Exec(artifactDocsSchema); err != nil {
		return err
	}
	ctx := context.Background()
	for after := (artifactKey{ts: -1}); ; {
		var batch []TurnChanges
		var ids []int64
		rows, err := s.w.Query(`SELECT tc.id, tc.ts, tc.msg, tc.agent, tc.repo, tc.files FROM turn_changes tc
			WHERE (tc.ts, tc.id) > (?, ?) ORDER BY tc.ts, tc.id LIMIT 500`, after.ts, after.id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c TurnChanges
			var id, ts int64
			var files string
			if err := rows.Scan(&id, &ts, &c.Msg, &c.Agent, &c.Repo, &files); err != nil {
				rows.Close()
				return err
			}
			c.At = fromMS(ts)
			_ = json.Unmarshal([]byte(files), &c.Files)
			batch, ids = append(batch, c), append(ids, id)
			after = artifactKey{ts: ts, id: id}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		err = s.tx(ctx, func(t *sql.Tx) error {
			for i, c := range batch {
				var thread string
				_ = t.QueryRowContext(ctx, "SELECT thread FROM messages WHERE id = ?", c.Msg).Scan(&thread)
				if err := applyChanges(ctx, t, c, ids[i], thread); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
}

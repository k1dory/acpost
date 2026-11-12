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
CREATE TABLE IF NOT EXISTS artifact_docs_built (done INTEGER NOT NULL);
-- The replay of changes in their order (a migration, a rebuild), and the reply files' listing:
-- index searches, not scans and sorts.
CREATE INDEX IF NOT EXISTS turn_changes_ts ON turn_changes (ts, id);
CREATE INDEX IF NOT EXISTS messages_reply_ts ON messages ((CASE WHEN finished_at > 0 THEN finished_at ELSE created_at END), seq);
CREATE INDEX IF NOT EXISTS files_reply ON files (msg, reply, id);
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

// docTurn is what of a turn's changes the projection uses.
type docTurn struct {
	change           int64
	ts               int64
	msg, agent, repo string
	files            []ChangedFile
}

func isRename(f ChangedFile) bool {
	return (strings.HasPrefix(f.Status, "R") || strings.HasPrefix(f.Status, "C")) && f.From != ""
}

// putDoc writes a path's row.
func putDoc(ctx context.Context, t *sql.Tx, path string, k artifactKey, c docTurn, thread, status string, made bool) error {
	_, err := t.ExecContext(ctx, `INSERT INTO artifact_docs (path, ts, change, idx, msg, thread, agent, status, made, listed)
		VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT (path) DO UPDATE SET ts = excluded.ts, change = excluded.change, idx = excluded.idx,
		msg = excluded.msg, thread = excluded.thread, agent = excluded.agent, status = excluded.status, made = excluded.made, listed = excluded.listed`,
		path, k.ts, k.id, k.idx, c.msg, thread, c.agent, status, made, isDoc(path, status, made))
	return err
}

// applyFile applies one change of a turn, in order after every change the rows it reads have.
func applyFile(ctx context.Context, t *sql.Tx, c docTurn, i int, thread string) error {
	f := c.files[i]
	k := artifactKey{ts: c.ts, id: c.change, idx: int64(i)}
	w := wholePath(c.repo, f.Path)
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
	case isRename(f):
		sw := wholePath(c.repo, f.From)
		src, err := getDoc(ctx, t, sw)
		if err != nil {
			return err
		}
		made = src.madeNow()
		if strings.HasPrefix(f.Status, "R") && src != nil { // a rename retires its source
			if err := putDoc(ctx, t, sw, k, c, thread, "D", false); err != nil {
				return err
			}
		}
	}
	if docKept(f.Path) {
		return putDoc(ctx, t, w, k, c, thread, f.Status, made)
	}
	return nil
}

// touches says whether the projection reads or writes anything for a change.
func touches(f ChangedFile) bool { return f.Path != "" && (docKept(f.Path) || isRename(f)) }

// applyChanges updates the projection with one turn's changes (already stored), in their order.
// A turn recorded after a later one (two turns finishing out of order) is not applied on top:
// the paths it touches are rebuilt from their history (rebuildDocs) — the later turn may have
// renamed or changed them already, with or without a row to show it.
func applyChanges(ctx context.Context, t *sql.Tx, c docTurn, thread string) error {
	touched := map[string]bool{}
	for _, f := range c.files {
		if touches(f) {
			touched[wholePath(c.repo, f.Path)] = true
			if isRename(f) {
				touched[wholePath(c.repo, f.From)] = true
			}
		}
	}
	if len(touched) == 0 {
		return nil // source files only: nothing of them is kept
	}
	var later int
	if err := t.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT 1 FROM turn_changes WHERE (ts, id) > (?, ?) LIMIT 1)", c.ts, c.change).Scan(&later); err != nil {
		return err
	}
	if later > 0 {
		return rebuildDocs(ctx, t, touched, c.change)
	}
	for i, f := range c.files {
		if touches(f) {
			if err := applyFile(ctx, t, c, i, thread); err != nil {
				return err
			}
		}
	}
	return nil
}

// changesInOrder calls fn with every stored turn's changes, in their order (turn_changes_ts).
func changesInOrder(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, fn func(c docTurn) error) error {
	rows, err := q.QueryContext(ctx, "SELECT id, ts, msg, agent, repo, files FROM turn_changes ORDER BY ts, id")
	if err != nil {
		return err
	}
	var all []docTurn
	for rows.Next() {
		var c docTurn
		var files string
		if err := rows.Scan(&c.change, &c.ts, &c.msg, &c.agent, &c.repo, &files); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal([]byte(files), &c.files)
		all = append(all, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range all {
		if err := fn(c); err != nil {
			return err
		}
	}
	return nil
}

// rebuildDocs makes the rows of these paths again from the stored history, in order: the paths,
// and every path a rename links to them (a rename is read with both its ends). It reads the whole
// history twice — acceptable on this rare path (a turn recorded after a later one, late), never
// taken by a turn in order.
//
// What retention collected is not in the history, only in the rows. So the paths' state before
// their surviving history (a baseline) is inferred: the surviving history without the late turn,
// replayed from nothing made, must give what the rows say; where a row says made and that replay
// does not, a creation was collected — of that path, or, when its surviving history begins with
// a rename into it, of the rename's source. The rows are then the replay of all of it from that
// baseline.
func rebuildDocs(ctx context.Context, t *sql.Tx, seeds map[string]bool, late int64) error {
	type event struct {
		c docTurn
		i int
	}
	var links [][2]string
	var events []event
	err := changesInOrder(ctx, t, func(c docTurn) error {
		for i, f := range c.files {
			if !touches(f) {
				continue
			}
			events = append(events, event{c, i})
			if isRename(f) {
				links = append(links, [2]string{wholePath(c.repo, f.Path), wholePath(c.repo, f.From)})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	set := map[string]bool{}
	for p := range seeds {
		set[p] = true
	}
	for grew := true; grew; {
		grew = false
		for _, l := range links {
			if set[l[0]] != set[l[1]] {
				set[l[0]], set[l[1]], grew = true, true, true
			}
		}
	}
	var mine []event
	for _, e := range events {
		f := e.c.files[e.i]
		if set[wholePath(e.c.repo, f.Path)] || isRename(f) && set[wholePath(e.c.repo, f.From)] {
			mine = append(mine, e)
		}
	}
	old := map[string]*docRow{}
	for p := range set {
		if old[p], err = getDoc(ctx, t, p); err != nil {
			return err
		}
	}

	// The replay, in memory: what applyFile does to rows, to a map.
	type state struct {
		made, row bool // row: the projection would have a row for it
		status    string
		ev        event
		touched   bool
	}
	replay := func(base map[string]bool, skip int64) map[string]*state {
		st := map[string]*state{}
		at := func(p string) *state {
			if st[p] == nil {
				st[p] = &state{made: base[p], row: old[p] != nil}
			}
			return st[p]
		}
		now := func(x *state) bool {
			return x.row && x.made && !strings.HasPrefix(x.status, "D") || !x.touched && x.made
		}
		for _, e := range mine {
			if e.c.change == skip {
				continue
			}
			f := e.c.files[e.i]
			w := wholePath(e.c.repo, f.Path)
			d := at(w)
			made := now(d)
			switch {
			case f.Status == "A":
				made = true
			case strings.HasPrefix(f.Status, "D"):
				made = false
			case isRename(f):
				src := at(wholePath(e.c.repo, f.From))
				made = now(src)
				if strings.HasPrefix(f.Status, "R") && (src.row || src.touched && docKept(f.From)) {
					src.made, src.status, src.ev, src.touched, src.row = false, "D", e, true, true
				}
			}
			if docKept(f.Path) {
				d.made, d.status, d.ev, d.touched, d.row = made, f.Status, e, true, true
			}
		}
		return st
	}
	// Where a path's surviving history begins with a rename into it: its source.
	beganFrom := map[string]string{}
	seen := map[string]bool{}
	for _, e := range mine {
		f := e.c.files[e.i]
		w := wholePath(e.c.repo, f.Path)
		if !seen[w] {
			seen[w] = true
			if isRename(f) {
				beganFrom[w] = wholePath(e.c.repo, f.From)
			}
		}
		if isRename(f) {
			seen[wholePath(e.c.repo, f.From)] = true
		}
	}
	base := map[string]bool{}
	for range len(set) + 1 {
		got, grew := replay(base, late), false
		for p := range set {
			o, g := old[p], got[p]
			if !o.madeNow() || g != nil && g.touched && g.made && !strings.HasPrefix(g.status, "D") {
				continue
			}
			if g == nil || !g.touched {
				continue // nothing of it survives: its row stays
			}
			q := p
			for beganFrom[q] != "" && !base[q] {
				q = beganFrom[q]
			}
			if !base[q] {
				base[q], grew = true, true
			}
		}
		if !grew {
			break
		}
	}
	for p, x := range replay(base, 0) {
		if !set[p] || !x.touched {
			continue
		}
		if !docKept(p) {
			continue // only the kept types have rows
		}
		var thread string
		_ = t.QueryRowContext(ctx, "SELECT thread FROM messages WHERE id = ?", x.ev.c.msg).Scan(&thread)
		k := artifactKey{ts: x.ev.c.ts, id: x.ev.c.change, idx: int64(x.ev.i)}
		if err := putDoc(ctx, t, p, k, x.ev.c, thread, x.status, x.made); err != nil {
			return err
		}
	}
	return nil
}

// docsBatchSQL is a batch of the stored changes after a key, in order: a range of turn_changes_ts.
const docsBatchSQL = `SELECT id, ts, msg, agent, repo, files FROM turn_changes WHERE (ts, id) > (?, ?) ORDER BY ts, id LIMIT ?`

// docsBatch is how many turns' changes a transaction of the build applies; afterDocsBatch, in
// tests, cuts a build short.
var (
	docsBatch      = 500
	afterDocsBatch func() error
)

// buildArtifactDocs fills the projection from the changes a store already has, once: replayed in
// order, a batch at a time. It is built when artifact_docs_built says so, in the build's last
// transaction; a build cut short (a crash, an error) is not taken as done, and is made again from
// the start at the next open.
func (s *Store) buildArtifactDocs() error {
	if _, err := s.w.Exec(artifactDocsSchema); err != nil {
		return err
	}
	var built int
	if err := s.w.QueryRow("SELECT count(*) FROM artifact_docs_built").Scan(&built); err != nil || built > 0 {
		return err
	}
	if _, err := s.w.Exec("DELETE FROM artifact_docs"); err != nil {
		return err
	}
	ctx := context.Background()
	for after := (artifactKey{ts: -1 << 62}); ; {
		var batch []docTurn
		rows, err := s.w.Query(docsBatchSQL, after.ts, after.id, docsBatch)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c docTurn
			var files string
			if err := rows.Scan(&c.change, &c.ts, &c.msg, &c.agent, &c.repo, &files); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal([]byte(files), &c.files)
			batch = append(batch, c)
			after = artifactKey{ts: c.ts, id: c.change}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		err = s.tx(ctx, func(t *sql.Tx) error {
			for _, c := range batch {
				var thread string
				_ = t.QueryRowContext(ctx, "SELECT thread FROM messages WHERE id = ?", c.msg).Scan(&thread)
				for i, f := range c.files {
					if touches(f) {
						if err := applyFile(ctx, t, c, i, thread); err != nil {
							return err
						}
					}
				}
			}
			if len(batch) < docsBatch {
				_, err := t.ExecContext(ctx, "INSERT INTO artifact_docs_built (done) VALUES (1)")
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
		if len(batch) < docsBatch {
			return nil
		}
		if afterDocsBatch != nil {
			if err := afterDocsBatch(); err != nil {
				return err
			}
		}
	}
}

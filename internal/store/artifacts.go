package store

// The artifacts of every chat, for the app's library: the files agents attached to their replies
// (content kept here, or a link), and the documents their turns created or edited on disk (what
// turn_changes recorded), newest first.
//
// Of a turn's changes only documents a person looks at are listed — pages, Markdown, pictures,
// PDFs, tables (docExts), and JSON the turn created (an edited one is mostly configuration) — not
// every source file it touched; none in a hidden folder or node_modules. A file changed by many
// turns is listed once, as its latest change left it: not at all when that deleted it; a JSON file
// when some turn created it, whichever turn changed it last.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Artifact is a file an agent handed back with a reply, or a document a turn made or changed.
type Artifact struct {
	Kind   string    `json:"kind"`             // file (content kept here), link (a reply's link), change (a file a turn created or edited)
	Name   string    `json:"name"`             // a file's name; a change's base name
	Type   string    `json:"type,omitempty"`   // a file's or link's, as kept
	Size   int64     `json:"size"`             // -1 when not known
	URI    string    `json:"uri,omitempty"`    // a link's
	Path   string    `json:"path,omitempty"`   // a change's file, absolute (a link's to a file here, filled by the node)
	Status string    `json:"status,omitempty"` // a change's: A, M, R…
	Msg    string    `json:"msg"`
	Thread string    `json:"thread,omitempty"`
	Title  string    `json:"title,omitempty"` // the chat's
	Agent  string    `json:"agent,omitempty"`
	At     time.Time `json:"at"`
	Gone   bool      `json:"gone,omitempty"` // the file is no longer there (filled by the node)
}

// ArtifactQuery picks a page of artifacts: older than Before (a cursor a page gave), whose name
// or address has Match in it, at most Limit.
type ArtifactQuery struct {
	Before string
	Match  string
	Limit  int
}

// docExts are the extensions of the documents a turn's changes list; JSON only when created.
var docExts = []string{".html", ".htm", ".md", ".markdown", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".csv"}

// ErrBadCursor is a cursor no page gave.
var ErrBadCursor = errors.New("bad cursor")

// artifactKey orders artifacts: when, then reply files before changes at the same moment, then
// their rows. A cursor is the key of the last artifact a page had.
type artifactKey struct {
	ts      int64
	src     int // 1 a reply's file, 0 a turn's change
	id, idx int64
}

func (k artifactKey) String() string {
	return fmt.Sprintf("%d.%d.%d.%d", k.ts, k.src, k.id, k.idx)
}

func parseArtifactKey(s string) (artifactKey, error) {
	p := strings.Split(s, ".")
	if len(p) != 4 {
		return artifactKey{}, ErrBadCursor
	}
	var v [4]int64
	for i, x := range p {
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return artifactKey{}, ErrBadCursor
		}
		v[i] = n
	}
	if v[1] != 0 && v[1] != 1 {
		return artifactKey{}, ErrBadCursor
	}
	return artifactKey{ts: v[0], src: int(v[1]), id: v[2], idx: v[3]}, nil
}

// artifactsSQL lists both kinds as one ordered list. A change's whole path is its directory and
// its path from there, unless that path is whole already (a file outside the repository).
func artifactsSQL() string {
	var ext []string
	for _, e := range docExts {
		ext = append(ext, "lower(path) LIKE '%"+e+"'")
	}
	return `WITH ch AS (
		SELECT tc.id, CAST(j.key AS INTEGER) AS idx, tc.repo, tc.msg, tc.agent, tc.ts,
			coalesce(json_extract(j.value, '$.path'), '') AS path, coalesce(json_extract(j.value, '$.status'), '') AS status
		FROM turn_changes tc, json_each(tc.files) j
	), chf AS (
		SELECT ch.*, CASE WHEN path LIKE '/%' OR path LIKE '\%' OR path LIKE '_:%' THEN path ELSE repo || '/' || path END AS whole
		FROM ch WHERE path <> ''
	), ranked AS (
		-- A path's whole history first: its latest change is what it is now (a deletion: gone), and
		-- whether any turn created it.
		SELECT chf.*, ROW_NUMBER() OVER (PARTITION BY whole ORDER BY ts DESC, id DESC, idx DESC) AS rn,
			max(status = 'A') OVER (PARTITION BY whole) AS created
		FROM chf
	), docs AS (
		SELECT * FROM ranked WHERE rn = 1 AND status NOT LIKE 'D%'
			AND (` + strings.Join(ext, " OR ") + ` OR (created = 1 AND lower(path) LIKE '%.json'))
			AND '/' || replace(whole, '\', '/') NOT LIKE '%/.%' AND '/' || replace(whole, '\', '/') NOT LIKE '%/node_modules/%'
	), items AS (
		SELECT CASE WHEN m.finished_at > 0 THEN m.finished_at ELSE m.created_at END AS ts, 1 AS src, f.id AS id, 0 AS idx,
			f.name AS name, f.type AS type, f.size AS size, f.uri AS uri, '' AS whole, '' AS status,
			m.id AS msg, m.thread AS thread, m.recipient AS agent, CASE WHEN f.uri <> '' THEN f.uri ELSE f.name END AS addr
		FROM files f JOIN messages m ON m.seq = f.msg WHERE f.reply = 1
		UNION ALL
		SELECT d.ts, 0, d.id, d.idx, '', '', -1, '', d.whole, d.status, d.msg, coalesce(m.thread, ''), d.agent, d.whole
		FROM docs d LEFT JOIN messages m ON m.id = d.msg
	)
	SELECT ts, src, id, idx, name, type, size, uri, whole, status, msg, thread, agent FROM items
	WHERE (ts, src, id, idx) < (?, ?, ?, ?) AND (? = '' OR name LIKE ? ESCAPE '\' OR addr LIKE ? ESCAPE '\')
	ORDER BY ts DESC, src DESC, id DESC, idx DESC LIMIT ?`
}

// likePattern is s to find anywhere, its wildcards taken as themselves.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// Artifacts lists a page of artifacts, newest first, and the cursor of the next ("" for none).
func (s *Store) Artifacts(ctx context.Context, q ArtifactQuery) ([]Artifact, string, error) {
	if q.Limit <= 0 {
		q.Limit = 60
	}
	from := artifactKey{ts: 1 << 62, src: 1, id: 1 << 62, idx: 1 << 62}
	if q.Before != "" {
		k, err := parseArtifactKey(q.Before)
		if err != nil {
			return nil, "", err
		}
		from = k
	}
	match := strings.TrimSpace(q.Match)
	pat := likePattern(match)
	rows, err := s.r.QueryContext(ctx, artifactsSQL(), from.ts, from.src, from.id, from.idx, match, pat, pat, q.Limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Artifact
	var keys []artifactKey
	for rows.Next() {
		var a Artifact
		var k artifactKey
		var ts int64
		if err := rows.Scan(&ts, &k.src, &k.id, &k.idx, &a.Name, &a.Type, &a.Size, &a.URI, &a.Path, &a.Status, &a.Msg, &a.Thread, &a.Agent); err != nil {
			return nil, "", err
		}
		k.ts = ts
		a.At = fromMS(ts)
		switch {
		case k.src == 0:
			a.Kind = "change"
			a.Path = filepath.Clean(filepath.FromSlash(a.Path))
			a.Name = filepath.Base(a.Path)
		case a.URI != "":
			a.Kind = "link"
		default:
			a.Kind = "file"
		}
		out = append(out, a)
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > q.Limit {
		out = out[:q.Limit]
		next = keys[q.Limit-1].String()
	}
	if err := s.titleArtifacts(ctx, out); err != nil {
		return nil, "", err
	}
	return out, next, nil
}

// titleArtifacts gives each artifact its chat's title: the start of the thread's first message.
func (s *Store) titleArtifacts(ctx context.Context, as []Artifact) error {
	titles := map[string]string{}
	for i := range as {
		th := as[i].Thread
		if th == "" {
			continue
		}
		t, ok := titles[th]
		if !ok {
			err := s.r.QueryRowContext(ctx, `SELECT substr(c.body, 1, 200) FROM messages m JOIN contents c ON c.seq = m.seq
				WHERE m.thread = ? ORDER BY m.seq LIMIT 1`, th).Scan(&t)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			t = firstLine(t)
			titles[th] = t
		}
		as[i].Title = t
	}
	return nil
}

// ReplyFileHead is a file of a message's reply with at most max bytes of its content (all of it
// when it is no longer); Size is the whole file's. A link has no content.
func (s *Store) ReplyFileHead(ctx context.Context, id, name string, max int) (File, error) {
	var f File
	f.Reply = true
	err := s.r.QueryRowContext(ctx, `SELECT f.name, f.type, f.size, f.uri, f.hash, CASE WHEN f.hash = '' THEN NULL ELSE substr(b.data, 1, ?) END
		FROM files f JOIN messages m ON m.seq = f.msg LEFT JOIN blobs b ON b.hash = f.hash AND f.hash <> ''
		WHERE m.id = ? AND f.reply = 1 AND f.name = ?`, max, id, name).Scan(&f.Name, &f.Type, &f.Size, &f.URI, &f.SHA256, &f.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if f.Data == nil && f.URI == "" {
		f.Data = []byte{}
	}
	return f, err
}

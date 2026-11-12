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
//
// The documents are read from a projection kept with the changes (artifactdocs.go); reply files
// from their table. A filter matches both by one rule of case (fold).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
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
	Cwd    string    `json:"cwd,omitempty"`   // the chat's directory (filled by the node)
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

// replyFilesSQL lists the files of replies older than a cursor, whose name or address has the
// (folded) match in it.
const replyFilesSQL = `SELECT ts, id, name, type, size, uri, msg, thread, agent FROM (
		SELECT CASE WHEN m.finished_at > 0 THEN m.finished_at ELSE m.created_at END AS ts, f.id AS id,
			f.name AS name, f.type AS type, f.size AS size, f.uri AS uri, m.id AS msg, m.thread AS thread, m.recipient AS agent
		FROM files f JOIN messages m ON m.seq = f.msg WHERE f.reply = 1
	) WHERE (ts, 1, id, 0) < (?, ?, ?, ?) AND (? = '' OR instr(acpost_fold(name), ?) > 0 OR instr(acpost_fold(uri), ?) > 0)
	ORDER BY ts DESC, id DESC LIMIT ?`

// docsSQL lists the documents on disk older than a cursor, whose path has the (folded) match in
// it: an indexed read of the projection (artifactdocs.go).
const docsSQL = `SELECT ts, change, idx, path, status, msg, thread, agent FROM artifact_docs
	WHERE listed = 1 AND (ts < ? OR ts = ? AND (? = 1 OR (change, idx) < (?, ?)))
		AND (? = '' OR instr(acpost_fold(path), ?) > 0)
	ORDER BY ts DESC, change DESC, idx DESC LIMIT ?`

// wholePath is a change's path whole: its directory and its path from there, unless that path is
// whole already (a file outside the repository).
func wholePath(repo, p string) string {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || len(p) > 1 && p[1] == ':' {
		return p
	}
	return repo + "/" + p
}

// isDoc says whether a path as it is now is a document the library lists.
func isDoc(path, status string, made bool) bool {
	if strings.HasPrefix(status, "D") {
		return false
	}
	slashed := "/" + strings.ReplaceAll(path, `\`, "/")
	if strings.Contains(slashed, "/.") || strings.Contains(slashed, "/node_modules/") {
		return false
	}
	low := strings.ToLower(path)
	for _, e := range docExts {
		if strings.HasSuffix(low, e) {
			return true
		}
	}
	return made && strings.HasSuffix(low, ".json")
}

func (a artifactKey) less(b artifactKey) bool {
	if a.ts != b.ts {
		return a.ts < b.ts
	}
	if a.src != b.src {
		return a.src < b.src
	}
	if a.id != b.id {
		return a.id < b.id
	}
	return a.idx < b.idx
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
	type item struct {
		a Artifact
		k artifactKey
	}
	var all []item

	// Each source, a page's worth at most; merged, the page.
	m := fold(match)
	rows, err := s.r.QueryContext(ctx, replyFilesSQL, from.ts, from.src, from.id, from.idx, m, m, m, q.Limit+1)
	if err != nil {
		return nil, "", err
	}
	for rows.Next() {
		var a Artifact
		k := artifactKey{src: 1}
		if err := rows.Scan(&k.ts, &k.id, &a.Name, &a.Type, &a.Size, &a.URI, &a.Msg, &a.Thread, &a.Agent); err != nil {
			rows.Close()
			return nil, "", err
		}
		a.Kind = "file"
		if a.URI != "" {
			a.Kind = "link"
		}
		all = append(all, item{a, k})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	rows, err = s.r.QueryContext(ctx, docsSQL, from.ts, from.ts, from.src, from.id, from.idx, m, m, q.Limit+1)
	if err != nil {
		return nil, "", err
	}
	for rows.Next() {
		var a Artifact
		var k artifactKey
		var path string
		if err := rows.Scan(&k.ts, &k.id, &k.idx, &path, &a.Status, &a.Msg, &a.Thread, &a.Agent); err != nil {
			rows.Close()
			return nil, "", err
		}
		a.Kind, a.Size = "change", -1
		a.Path = filepath.Clean(filepath.FromSlash(path))
		a.Name = filepath.Base(a.Path)
		all = append(all, item{a, k})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	sort.Slice(all, func(i, j int) bool { return all[j].k.less(all[i].k) })

	var out []Artifact
	var keys []artifactKey
	for _, it := range all {
		if len(out) == q.Limit+1 {
			break
		}
		it.a.At = fromMS(it.k.ts)
		out = append(out, it.a)
		keys = append(keys, it.k)
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

// dirSQL is a message's directory as its meta says it: the session's, else the one asked for.
const dirSQL = `coalesce(nullif(json_extract(meta, '$.acp_session_cwd'), ''), nullif(json_extract(meta, '$.acp_cwd'), ''), '')`

// ChatDirs is the directory of each of these messages (by id) and of each of these threads: its
// root's when the root is kept and says one, else the first kept message's that does. Two
// queries, whatever the threads hold; only the messages' meta is read, never their bodies.
func (s *Store) ChatDirs(ctx context.Context, msgs, threads []string) (byMsg, byThread map[string]string, err error) {
	byMsg, byThread = map[string]string{}, map[string]string{}
	in := func(ids []string) (string, []any) {
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		return "(?" + strings.Repeat(",?", len(ids)-1) + ")", args
	}
	scan := func(q string, args []any, into map[string]string) error {
		rows, err := s.r.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, d string
			if err := rows.Scan(&k, &d); err != nil {
				return err
			}
			into[k] = d
		}
		return rows.Err()
	}
	if len(msgs) > 0 {
		list, args := in(msgs)
		if err := scan(`SELECT id, `+dirSQL+` FROM messages WHERE id IN `+list+` AND meta <> '' AND json_valid(meta)`, args, byMsg); err != nil {
			return nil, nil, err
		}
	}
	if len(threads) > 0 {
		list, args := in(threads)
		q := `SELECT thread, dir FROM (
				SELECT thread, dir, ROW_NUMBER() OVER (PARTITION BY thread ORDER BY id = thread DESC, seq) AS rn FROM (
					SELECT thread, id, seq, ` + dirSQL + ` AS dir FROM messages
					WHERE thread IN ` + list + ` AND meta <> '' AND json_valid(meta)
				) WHERE dir <> ''
			) WHERE rn = 1`
		if err := scan(q, args, byThread); err != nil {
			return nil, nil, err
		}
	}
	return byMsg, byThread, nil
}

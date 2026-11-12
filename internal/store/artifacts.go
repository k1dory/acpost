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

// replyFilesSQL lists the files of replies older than a cursor, matching a name or address.
const replyFilesSQL = `SELECT ts, id, name, type, size, uri, msg, thread, agent FROM (
		SELECT CASE WHEN m.finished_at > 0 THEN m.finished_at ELSE m.created_at END AS ts, f.id AS id,
			f.name AS name, f.type AS type, f.size AS size, f.uri AS uri, m.id AS msg, m.thread AS thread, m.recipient AS agent
		FROM files f JOIN messages m ON m.seq = f.msg WHERE f.reply = 1
	) WHERE (ts, 1, id, 0) < (?, ?, ?, ?) AND (? = '' OR name LIKE ? ESCAPE '\' OR (CASE WHEN uri <> '' THEN uri ELSE name END) LIKE ? ESCAPE '\')
	ORDER BY ts DESC, id DESC LIMIT ?`

// changeRowsSQL is every file a turn's changes name, without the diffs: what the library reads
// its documents from, oldest first.
const changeRowsSQL = `SELECT tc.ts, tc.id, CAST(j.key AS INTEGER), tc.repo, tc.msg, tc.agent,
		coalesce(json_extract(j.value, '$.path'), ''), coalesce(json_extract(j.value, '$.status'), ''), coalesce(json_extract(j.value, '$.from'), '')
	FROM turn_changes tc, json_each(tc.files) j ORDER BY tc.ts, tc.id, j.key`

// docState is a path as the turns' changes left it.
type docState struct {
	key          artifactKey
	path, status string
	msg, agent   string
	made         bool // a turn made the file there now: created it, or renamed or copied there one it made
}

// wholePath is a change's path whole: its directory and its path from there, unless that path is
// whole already (a file outside the repository).
func wholePath(repo, p string) string {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || len(p) > 1 && p[1] == ':' {
		return p
	}
	return repo + "/" + p
}

// isDoc says whether a path as it is now is a document the library lists.
func isDoc(d *docState) bool {
	if strings.HasPrefix(d.status, "D") {
		return false
	}
	slashed := "/" + strings.ReplaceAll(d.path, `\`, "/")
	if strings.Contains(slashed, "/.") || strings.Contains(slashed, "/node_modules/") {
		return false
	}
	low := strings.ToLower(d.path)
	for _, e := range docExts {
		if strings.HasSuffix(low, e) {
			return true
		}
	}
	return d.made && strings.HasSuffix(low, ".json")
}

// documents reads the turns' changes once and replays them in order: each path ends as its latest
// change left it (deleted, or renamed away: gone), and what a turn made follows a rename or copy
// only forward in time, from a file there at that moment. One pass, whatever the history holds.
func (s *Store) documents(ctx context.Context) ([]*docState, error) {
	rows, err := s.r.QueryContext(ctx, changeRowsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	state := map[string]*docState{}
	at := func(w string) *docState {
		d := state[w]
		if d == nil {
			d = &docState{path: w}
			state[w] = d
		}
		return d
	}
	for rows.Next() {
		var k artifactKey
		var repo, msg, agent, p, status, from string
		if err := rows.Scan(&k.ts, &k.id, &k.idx, &repo, &msg, &agent, &p, &status, &from); err != nil {
			return nil, err
		}
		if p == "" {
			continue
		}
		d := at(wholePath(repo, p))
		made := d.made
		switch {
		case status == "A":
			made = true
		case strings.HasPrefix(status, "D"):
			made = false
		case (strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")) && from != "":
			src := at(wholePath(repo, from))
			made = src.made
			if strings.HasPrefix(status, "R") { // a rename retires its source
				src.key, src.status, src.msg, src.agent, src.made = k, "D", msg, agent, false
			}
		}
		d.key, d.status, d.msg, d.agent, d.made = k, status, msg, agent, made
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []*docState
	for _, d := range state {
		if isDoc(d) {
			out = append(out, d)
		}
	}
	return out, nil
}

// likePattern is s to find anywhere, its wildcards taken as themselves.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
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

	// Reply files: the page's worth, at most.
	pat := likePattern(match)
	rows, err := s.r.QueryContext(ctx, replyFilesSQL, from.ts, from.src, from.id, from.idx, match, pat, pat, q.Limit+1)
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

	// Documents on disk: those older than the cursor, matching.
	docs, err := s.documents(ctx)
	if err != nil {
		return nil, "", err
	}
	low := strings.ToLower(match)
	for _, d := range docs {
		if !d.key.less(from) {
			continue
		}
		p := filepath.Clean(filepath.FromSlash(d.path))
		name := filepath.Base(p)
		if low != "" && !strings.Contains(strings.ToLower(name), low) && !strings.Contains(strings.ToLower(d.path), low) {
			continue
		}
		all = append(all, item{Artifact{Kind: "change", Name: name, Size: -1, Path: p, Status: d.status, Msg: d.msg, Agent: d.agent}, d.key})
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
	if err := s.threadsOf(ctx, out); err != nil {
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

// threadsOf fills in the threads of the documents of a page, by their messages (one look for
// the page).
func (s *Store) threadsOf(ctx context.Context, as []Artifact) error {
	var ids []any
	for _, a := range as {
		if a.Kind == "change" {
			ids = append(ids, a.Msg)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.r.QueryContext(ctx, "SELECT id, thread FROM messages WHERE id IN (?"+strings.Repeat(",?", len(ids)-1)+")", ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	thread := map[string]string{}
	for rows.Next() {
		var id, th string
		if err := rows.Scan(&id, &th); err != nil {
			return err
		}
		thread[id] = th
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range as {
		if as[i].Kind == "change" {
			as[i].Thread = thread[as[i].Msg]
		}
	}
	return nil
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

// Package store keeps messages, their events and agents' ACP sessions in one SQLite file.
//
// One process owns a store (a lock file says so). Writes go through a single connection, so
// concurrent writers wait their turn in the process instead of failing with SQLITE_BUSY; reads have
// their own connections and, in WAL mode, never wait for a write. Bodies live in their own table,
// so listing messages reads small rows whatever the bodies weigh.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrLeaseLost = errors.New("lease lost: the message is no longer held by this claim")
	ErrFinal     = errors.New("the message is already final")
	ErrLocked    = errors.New("the store is in use by another node")
	ErrStoreFull = errors.New("the store is full (limits.max_store)")
)

// State of a message.
type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Done      State = "done"
	Failed    State = "failed"
	Cancelled State = "cancelled"
	Expired   State = "expired"
)

// Final says whether nothing more will happen to a message in this state.
func (s State) Final() bool { return s == Done || s == Failed || s == Cancelled || s == Expired }

// Valid says whether s is a state.
func (s State) Valid() bool { return s == Queued || s == Running || s.Final() }

// Message is a message as stored. Lists leave Body and Reply empty (BodySize and ReplySize say how
// big they are).
type Message struct {
	ID         string            `json:"id"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Task       string            `json:"task,omitempty"`
	Thread     string            `json:"thread"`
	Parent     string            `json:"parent,omitempty"`
	Depth      int               `json:"depth"`
	State      State             `json:"state"`
	Attempts   int               `json:"attempts"`
	Meta       map[string]string `json:"meta,omitempty"`
	Body       string            `json:"body,omitempty"`
	Reply      string            `json:"reply,omitempty"`
	Error      string            `json:"error,omitempty"`
	ErrorCode  string            `json:"error_code,omitempty"` // stable: session_busy, turn_timeout… (README)
	Hint       string            `json:"hint,omitempty"`       // what to do about the error; filled in by the node, not stored
	Files      Files             `json:"files,omitempty"`      // attached to the message and to its reply (Get lists them, without data)
	BodySize   int               `json:"body_size"`
	ReplySize  int               `json:"reply_size"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	ExpiresAt  time.Time         `json:"expires_at,omitzero"`
	FinishedAt time.Time         `json:"finished_at,omitzero"`
	Origin     string            `json:"origin,omitempty"` // the hub a message came from, or went to
	IdemKey    string            `json:"-"`
	LeaseID    string            `json:"lease,omitempty"`
	LeaseUntil time.Time         `json:"lease_until,omitzero"`
}

// Event is something that happened while a message was handled: a tool call, a permission
// decision, a notice.
type Event struct {
	Seq  int64     `json:"seq"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Data string    `json:"data"`
}

// Filter selects messages for List. Empty fields do not filter.
type Filter struct {
	To, From, Task, Thread string
	State                  State
	// Party, when set, keeps messages it may see: sent by its Sender, or to one of its Recipients.
	Party *Party
	After string // the cursor: the id of the last message of the previous page
	Limit int
}

// MaxPage is the most messages one List returns.
const MaxPage = 500

const truncMark = "…[truncated]"

// Options are the store's limits. Zero values take the defaults.
type Options struct {
	MaxStore            int64 // bytes of live data above which Insert refuses; 0: no limit
	JournalSizeLimit    int64 // the WAL is truncated to this after a checkpoint (default 64 MiB)
	MaxEventsPerMessage int   // default 1000
	MaxEventData        int   // bytes of one event's data (default 16 KiB)
}

// Store is an open store.
type Store struct {
	path   string // "" for memory
	w, r   *sql.DB
	lock   *os.File
	opt    Options
	closed atomic.Bool

	maintainMu sync.Mutex
	// written counts bytes written since the WAL was last checked (see afterWrite).
	written atomic.Int64
}

var memSeq atomic.Int64

// Open opens the store a DSN names: sqlite:///path/to/file.db or memory: (a private, in-memory
// store for tests). The file and its directory are created private to the user.
func Open(dsn string, opt Options) (*Store, error) {
	if opt.JournalSizeLimit == 0 {
		opt.JournalSizeLimit = 64 << 20
	}
	if opt.MaxEventsPerMessage == 0 {
		opt.MaxEventsPerMessage = 1000
	}
	if opt.MaxEventData == 0 {
		opt.MaxEventData = 16 << 10
	}
	s := &Store{opt: opt}
	// Foreign keys stay off: every delete removes its dependent rows itself, in the same
	// transaction, and checks on each write would cost the queue for no case it misses.
	pragmas := []string{"busy_timeout(10000)", "foreign_keys(0)", fmt.Sprintf("journal_size_limit(%d)", opt.JournalSizeLimit)}
	switch {
	case dsn == "memory:":
		name := fmt.Sprintf("file:acpost-mem-%d?mode=memory&cache=shared", memSeq.Add(1))
		db, err := sql.Open("sqlite", name+pragmaQuery(pragmas, "&"))
		if err != nil {
			return nil, err
		}
		// One connection: an in-memory database has no WAL, and readers would contend for its locks.
		db.SetMaxOpenConns(1)
		s.w, s.r = db, db
	case strings.HasPrefix(dsn, "sqlite://"):
		path := strings.TrimPrefix(dsn, "sqlite://")
		if path == "" {
			return nil, fmt.Errorf("store %q: no path", dsn)
		}
		if err := s.openFile(path, pragmas); err != nil {
			// What openFile took (the lock, the connections) goes, or the next Open says ErrLocked.
			s.Close()
			return nil, err
		}
	default:
		return nil, fmt.Errorf("store %q: sqlite:///path or memory:", dsn)
	}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func pragmaQuery(pragmas []string, sep string) string {
	var b strings.Builder
	for _, p := range pragmas {
		b.WriteString(sep + "_pragma=" + url.QueryEscape(p))
		sep = "&"
	}
	return b.String()
}

func (s *Store) openFile(path string, pragmas []string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if err := PrivateDir(dir); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("%s: %w", path, ErrLocked)
		}
		return err
	}
	s.lock, s.path = lock, path
	// SQLite gives the WAL and shared-memory files the database file's mode: create it private.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	// An existing file keeps its mode: make it, and SQLite's side files, private.
	for _, p := range []string{path, path + "-wal", path + "-shm", path + ".lock"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	base := "file:" + path + "?_txlock=immediate"
	// auto_vacuum must be set before the first table exists; on an existing file it is a no-op.
	w, err := sql.Open("sqlite", base+pragmaQuery(append([]string{"auto_vacuum(incremental)", "journal_mode(wal)", "synchronous(normal)"}, pragmas...), "&"))
	if err != nil {
		return err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	s.w = w
	// Make sure the file is in WAL mode before readers open it.
	if err := w.Ping(); err != nil {
		return err
	}
	r, err := sql.Open("sqlite", "file:"+path+"?_txlock=deferred"+pragmaQuery(append([]string{"query_only(1)"}, pragmas...), "&"))
	if err != nil {
		return err
	}
	r.SetMaxOpenConns(8)
	r.SetMaxIdleConns(8)
	s.r = r
	return nil
}

// PrivateDir refuses a directory other users can open: the store and the socket live in it.
func PrivateDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is open to other users (mode %o): use a private directory (chmod 700)", dir, st.Mode().Perm())
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	seq         INTEGER PRIMARY KEY,
	id          TEXT NOT NULL UNIQUE,
	sender      TEXT NOT NULL,
	recipient   TEXT NOT NULL,
	task        TEXT NOT NULL DEFAULT '',
	thread      TEXT NOT NULL,
	parent      TEXT NOT NULL DEFAULT '',
	depth       INTEGER NOT NULL DEFAULT 0,
	state       TEXT NOT NULL,
	attempts    INTEGER NOT NULL DEFAULT 0,
	meta        TEXT NOT NULL DEFAULT '',
	error       TEXT NOT NULL DEFAULT '',
	error_code  TEXT NOT NULL DEFAULT '',
	body_size   INTEGER NOT NULL,
	reply_size  INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL DEFAULT 0,
	finished_at INTEGER NOT NULL DEFAULT 0,
	origin      TEXT NOT NULL DEFAULT '',
	idem_key    TEXT,
	lease_id    TEXT NOT NULL DEFAULT '',
	lease_until INTEGER NOT NULL DEFAULT 0,
	events      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS messages_queue    ON messages (recipient, state, id);
CREATE INDEX IF NOT EXISTS messages_to       ON messages (recipient, id);
CREATE INDEX IF NOT EXISTS messages_from     ON messages (sender, id);
CREATE INDEX IF NOT EXISTS messages_task     ON messages (task, id) WHERE task <> '';
CREATE INDEX IF NOT EXISTS messages_thread   ON messages (thread, id);
CREATE INDEX IF NOT EXISTS messages_state    ON messages (state, finished_at);
CREATE INDEX IF NOT EXISTS messages_lease    ON messages (lease_until) WHERE state = 'running';
CREATE INDEX IF NOT EXISTS messages_origin   ON messages (origin, state) WHERE origin <> '';
CREATE UNIQUE INDEX IF NOT EXISTS messages_idem ON messages (sender, idem_key) WHERE idem_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS contents (
	seq   INTEGER PRIMARY KEY,
	body  TEXT NOT NULL,
	reply TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS events (
	msg  INTEGER NOT NULL,
	seq  INTEGER NOT NULL,
	ts   INTEGER NOT NULL,
	kind TEXT NOT NULL,
	data TEXT NOT NULL,
	PRIMARY KEY (msg, seq)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS sessions (
	agent      TEXT NOT NULL,
	key        TEXT NOT NULL,
	session_id TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (agent, key)
) WITHOUT ROWID;
`

func (s *Store) migrate() error {
	if _, err := s.w.Exec(schema); err != nil {
		return err
	}
	if err := migrateFiles(s.w); err != nil {
		return fmt.Errorf("migrate files: %w", err)
	}
	if _, err := s.w.Exec(filesSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(usageSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(changesSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(foldersSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(mcpSchema); err != nil {
		return err
	}
	if err := migrateMCP(s); err != nil {
		return fmt.Errorf("migrate mcp_servers: %w", err)
	}
	if _, err := s.w.Exec(playbooksSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(backlogSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(wikiSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(knowledgeSchema); err != nil {
		return err
	}
	if _, err := s.w.Exec(schedulesSchema); err != nil {
		return err
	}
	if err := migrateSchedules(s); err != nil {
		return fmt.Errorf("migrate schedules: %w", err)
	}
	// Columns added after the first release.
	for _, c := range []struct{ name, ddl string }{
		{"error_code", "error_code TEXT NOT NULL DEFAULT ''"},
		{"relayed", "relayed INTEGER NOT NULL DEFAULT 0"}, // events taken from the node running the agent
		// What Complete ended the message with (completion.go): the holder's repeat is no conflict.
		{"completion", "completion TEXT NOT NULL DEFAULT ''"},
		// The last hub event a forwarded message's copy took (hubcursor.go); 0: none taken by number.
		{"hub_seq", "hub_seq INTEGER NOT NULL DEFAULT 0"},
	} {
		var n int
		if err := s.w.QueryRow("SELECT count(*) FROM pragma_table_info('messages') WHERE name = ?", c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.w.Exec("ALTER TABLE messages ADD COLUMN " + c.ddl); err != nil {
				return err
			}
		}
	}
	// A message turning final publishes its turns' counted usage (usage_round1.go).
	if _, err := s.w.Exec(usagePublishSchema); err != nil {
		return err
	}
	return nil
}

// Path is the database file ("" for a memory store).
func (s *Store) Path() string { return s.path }

// Close closes the store and releases its lock. Closing twice is harmless.
func (s *Store) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	var err error
	if s.r != nil && s.r != s.w {
		err = s.r.Close()
	}
	if s.w != nil {
		if s.path != "" {
			// Leave a small file behind: fold the WAL into the database.
			_, _ = s.w.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		}
		err = errors.Join(err, s.w.Close())
	}
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
	}
	return err
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func newLease() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// tx runs fn in a write transaction (BEGIN IMMEDIATE: the write lock is taken up front).
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	t, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(t); err != nil {
		_ = t.Rollback()
		return err
	}
	if err := t.Commit(); err != nil {
		return err
	}
	s.afterWrite(ctx, 4096)
	return nil
}

// afterWrite keeps the WAL bounded. SQLite's own checkpoints after a commit are passive: while
// readers keep reading they cannot reset the WAL, and it grows. So once about a quarter of
// journal_size_limit has been written, the WAL's size is checked, and above the limit a
// TRUNCATE checkpoint waits for the readers of old snapshots and resets it.
//
// It runs on the caller's goroutine and holds the one writer connection, so it waits at most
// checkpointWait, not busy_timeout (10 s): a reader that keeps its snapshot longer leaves the WAL
// as it is until the next check, and no send waits out that reader.
func (s *Store) afterWrite(ctx context.Context, n int64) {
	if s.path == "" || s.written.Add(n) < s.opt.JournalSizeLimit/4 {
		return
	}
	s.written.Store(0)
	st, err := os.Stat(s.path + "-wal")
	if err != nil || st.Size() <= s.opt.JournalSizeLimit {
		return
	}
	c, err := s.w.Conn(ctx)
	if err != nil {
		return
	}
	defer c.Close()
	shortWait(ctx, c, checkpointWait, func() {
		var busy, logPages, done int
		_ = c.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &done)
	})
}

// liveBytes is how much of the file holds data (pages in use).
func liveBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var pages, free, size int64
	if err := q.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return 0, err
	}
	if err := q.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
		return 0, err
	}
	if err := q.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return 0, err
	}
	return (pages - free) * size, nil
}

// Insert stores a new message (state queued). A message with the IdemKey of an earlier message of
// the same sender is not stored again: the earlier one is returned with created false. The
// message's ID, From, To and Body are required; Thread defaults to the ID.
func (s *Store) Insert(ctx context.Context, m *Message) (Message, bool, error) {
	return s.InsertLimited(ctx, m, 0)
}

// ErrQueueFull is returned by InsertLimited when the recipient has maxQueued messages queued.
var ErrQueueFull = errors.New("queue full")

// InsertLimited is Insert that refuses (ErrQueueFull) when the recipient already has maxQueued
// messages queued (0: no limit). The idempotency key is looked up first, so a repeat of a message
// that filled the queue still returns it.
func (s *Store) InsertLimited(ctx context.Context, m *Message, maxQueued int) (Message, bool, error) {
	if m.ID == "" || m.From == "" || m.To == "" {
		return Message{}, false, errors.New("store: insert needs id, from and to")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	out := *m
	out.State, out.Attempts, out.CreatedAt, out.UpdatedAt = Queued, 0, now, now
	if out.Thread == "" {
		out.Thread = out.ID
	}
	out.BodySize, out.Reply, out.ReplySize, out.LeaseID = len(out.Body), "", 0, ""
	var meta string
	if len(out.Meta) > 0 {
		b, _ := json.Marshal(out.Meta)
		meta = string(b)
	}
	var idem any
	if out.IdemKey != "" {
		idem = out.IdemKey
	}
	var existing string
	err := s.tx(ctx, func(t *sql.Tx) error {
		if idem != nil {
			err := t.QueryRowContext(ctx, "SELECT id FROM messages WHERE sender = ? AND idem_key = ?", out.From, idem).Scan(&existing)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if maxQueued > 0 {
			var q int
			if err := t.QueryRowContext(ctx, `SELECT count(*) FROM messages INDEXED BY messages_queue WHERE recipient = ? AND state = 'queued'`, out.To).Scan(&q); err != nil {
				return err
			}
			if q >= maxQueued {
				return ErrQueueFull
			}
		}
		if err := s.roomFor(ctx, t, int64(len(out.Body))); err != nil { // and putFiles for new content
			return err
		}
		res, err := t.ExecContext(ctx, `INSERT INTO messages
			(id, sender, recipient, task, thread, parent, depth, state, meta, body_size, created_at, updated_at, expires_at, origin, idem_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			out.ID, out.From, out.To, out.Task, out.Thread, out.Parent, out.Depth, out.State, meta, out.BodySize,
			ms(now), ms(now), ms(out.ExpiresAt), out.Origin, idem)
		if err != nil {
			return err
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = t.ExecContext(ctx, "INSERT INTO contents (seq, body) VALUES (?, ?)", seq, out.Body); err != nil {
			return err
		}
		return s.putFiles(ctx, t, seq, false, out.Files)
	})
	if err != nil {
		return Message{}, false, err
	}
	s.afterWrite(ctx, int64(len(out.Body))+filesSize(out.Files))
	if existing != "" {
		prev, err := s.Get(ctx, existing)
		return prev, false, err
	}
	out.Files = withoutData(out.Files)
	return out, true, nil
}

const cols = `m.id, m.sender, m.recipient, m.task, m.thread, m.parent, m.depth, m.state, m.attempts, m.meta, m.error, m.error_code,
	m.body_size, m.reply_size, m.created_at, m.updated_at, m.expires_at, m.finished_at, m.origin, m.lease_id, m.lease_until`

type scanner interface{ Scan(...any) error }

func scanMessage(sc scanner, extra ...any) (Message, error) {
	var m Message
	var meta string
	var created, updated, expires, finished, leaseUntil int64
	dest := append([]any{&m.ID, &m.From, &m.To, &m.Task, &m.Thread, &m.Parent, &m.Depth, &m.State, &m.Attempts, &meta, &m.Error, &m.ErrorCode,
		&m.BodySize, &m.ReplySize, &created, &updated, &expires, &finished, &m.Origin, &m.LeaseID, &leaseUntil}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return m, err
	}
	if meta != "" {
		_ = json.Unmarshal([]byte(meta), &m.Meta)
	}
	m.CreatedAt, m.UpdatedAt, m.ExpiresAt, m.FinishedAt, m.LeaseUntil =
		fromMS(created), fromMS(updated), fromMS(expires), fromMS(finished), fromMS(leaseUntil)
	return m, nil
}

// ByIdem returns the message a sender sent with an idempotency key.
func (s *Store) ByIdem(ctx context.Context, sender, key string) (Message, error) {
	var id string
	err := s.r.QueryRowContext(ctx, "SELECT id FROM messages WHERE sender = ? AND idem_key = ?", sender, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return s.Get(ctx, id)
}

// Get returns a message with its body and reply.
func (s *Store) Get(ctx context.Context, id string) (Message, error) {
	return s.get(ctx, s.r, id)
}

func (s *Store) get(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	querier
}, id string) (Message, error) {
	var body, reply string
	m, err := scanMessage(q.QueryRowContext(ctx, "SELECT "+cols+", c.body, c.reply FROM messages m JOIN contents c ON c.seq = m.seq WHERE m.id = ?", id), &body, &reply)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.Body, m.Reply = body, reply
	m.Files, err = listFiles(ctx, q, id, false)
	return m, err
}

// withoutData lists files without their data.
func withoutData(files []File) []File {
	if len(files) == 0 {
		return nil
	}
	out := make([]File, len(files))
	for i, f := range files {
		if !f.Link() {
			f.Size, f.SHA256 = len(files[i].Data), Hash(files[i].Data)
		}
		f.Data = nil
		out[i] = f
	}
	return out
}

// List returns a page of messages, oldest first, without bodies; next is the cursor of the
// following page ("" on the last).
func (s *Store) List(ctx context.Context, f Filter) ([]Message, string, error) {
	if f.Limit <= 0 || f.Limit > MaxPage {
		f.Limit = MaxPage
	}
	var where []string
	var args []any
	add := func(cond string, v any) { where = append(where, cond); args = append(args, v) }
	if f.To != "" {
		add("m.recipient = ?", f.To)
	}
	if f.From != "" {
		add("m.sender = ?", f.From)
	}
	if f.Task != "" {
		add("m.task = ?", f.Task)
	}
	if f.Thread != "" {
		add("m.thread = ?", f.Thread)
	}
	if f.State != "" {
		add("m.state = ?", f.State)
	}
	if f.After != "" {
		add("m.id > ?", f.After)
	}
	if f.Party != nil {
		cond, pargs := f.Party.where("m.")
		where = append(where, cond)
		args = append(args, pargs...)
	}
	q := "SELECT " + cols + " FROM messages m"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY m.id LIMIT ?"
	args = append(args, f.Limit+1)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]Message, 0, min(f.Limit, 64))
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, "", err
		}
		m.LeaseID = ""
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// Claim takes the oldest queued message for recipient: it becomes running under a new lease that
// lasts lease. ok is false when there is none.
func (s *Store) Claim(ctx context.Context, recipient string, lease time.Duration) (Message, bool, error) {
	return s.ClaimAny(ctx, []string{recipient}, lease)
}

// ClaimAny is Claim for the oldest queued message for any of the recipients.
func (s *Store) ClaimAny(ctx context.Context, recipients []string, lease time.Duration) (Message, bool, error) {
	if len(recipients) == 0 {
		return Message{}, false, nil
	}
	// A lease of nothing has run out already: the next reap would requeue the message at once.
	if lease <= 0 {
		return Message{}, false, fmt.Errorf("store: claim needs a positive lease, not %v", lease)
	}
	now := time.Now()
	var m Message
	ph := strings.TrimSuffix(strings.Repeat("?,", len(recipients)), ",")
	args := make([]any, 0, len(recipients)+1)
	for _, r := range recipients {
		args = append(args, r)
	}
	args = append(args, ms(now))
	err := s.tx(ctx, func(t *sql.Tx) error {
		var id string
		err := t.QueryRowContext(ctx, `SELECT id FROM messages INDEXED BY messages_queue
			WHERE recipient IN (`+ph+`) AND state = 'queued' AND (expires_at = 0 OR expires_at > ?) ORDER BY id LIMIT 1`,
			args...).Scan(&id)
		if err != nil {
			return err
		}
		lid := newLease()
		// Still queued: the one writer and BEGIN IMMEDIATE make it so today; the condition keeps
		// a second claim of the same message impossible should either change.
		res, err := t.ExecContext(ctx, `UPDATE messages SET state = 'running', attempts = attempts + 1, lease_id = ?, lease_until = ?, updated_at = ?
			WHERE id = ? AND state = 'queued'`, lid, ms(now.Add(lease)), ms(now), id)
		if err != nil {
			return err
		}
		if k, err := res.RowsAffected(); err != nil {
			return err
		} else if k == 0 {
			return sql.ErrNoRows
		}
		// What an earlier attempt attached to its reply is not this attempt's.
		if _, err := t.ExecContext(ctx, "DELETE FROM files WHERE reply = 1 AND msg = (SELECT seq FROM messages WHERE id = ?)", id); err != nil {
			return err
		}
		m, err = s.get(ctx, t, id)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	return m, true, nil
}

// Settle copies the outcome of a message handled elsewhere (a hub) onto its local copy: state,
// reply, error and attempts. A local copy already final is left alone.
func (s *Store) Settle(ctx context.Context, id string, state State, reply, errCode, errText string, attempts int, files ...File) error {
	now := ms(time.Now())
	finished := int64(0)
	if state.Final() {
		finished = now
	}
	return s.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE messages SET state = ?, error = ?, error_code = ?, reply_size = ?, attempts = ?, updated_at = ?, finished_at = ?
			WHERE id = ? AND state IN ('queued', 'running')`, state, errText, errCode, len(reply), attempts, now, finished, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		_, err = t.ExecContext(ctx, "UPDATE contents SET reply = ? WHERE seq = (SELECT seq FROM messages WHERE id = ?)", reply, id)
		if err != nil || len(files) == 0 {
			return err
		}
		var seq int64
		if err := t.QueryRowContext(ctx, "SELECT seq FROM messages WHERE id = ?", id).Scan(&seq); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, "DELETE FROM files WHERE msg = ? AND reply = 1", seq); err != nil {
			return err
		}
		return s.putFiles(ctx, t, seq, true, files)
	})
}

// SetOrigin changes where a message came from (a joined node records the hub's latest lease).
func (s *Store) SetOrigin(ctx context.Context, id, origin string) error {
	_, err := s.w.ExecContext(ctx, "UPDATE messages SET origin = ? WHERE id = ?", origin, id)
	return err
}

// Unsettled lists the messages whose origin starts with prefix and that are not final, with their
// origins (a node resumes following them after a restart).
func (s *Store) Unsettled(ctx context.Context, prefix string) ([][2]string, error) {
	return s.UnsettledPage(ctx, prefix, "", MaxPage)
}

// UnsettledPage is a page of Unsettled after the id after.
func (s *Store) UnsettledPage(ctx context.Context, prefix, after string, limit int) ([][2]string, error) {
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, origin FROM messages WHERE origin <> '' AND substr(origin, 1, ?) = ?
		AND state IN ('queued', 'running') AND id > ? ORDER BY id LIMIT ?`, len(prefix), prefix, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var id, origin string
		if err := rows.Scan(&id, &origin); err != nil {
			return nil, err
		}
		out = append(out, [2]string{id, origin})
	}
	return out, rows.Err()
}

// Complete ends a running message held under lease: state is final, reply and errText are what
// came of it.
func (s *Store) Complete(ctx context.Context, id, lease string, state State, reply, errCode, errText string, files ...File) error {
	if !state.Final() {
		return fmt.Errorf("store: complete with state %s", state)
	}
	now := ms(time.Now())
	key := completionKey(lease, state, reply, errCode, errText, files)
	err := s.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE messages SET state = ?, error = ?, error_code = ?, reply_size = ?, lease_id = '', lease_until = 0,
			completion = ?, updated_at = ?, finished_at = ? WHERE id = ? AND state = 'running' AND lease_id = ? AND lease_id <> ''`,
			state, errText, errCode, len(reply), key, now, now, id, lease)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// A holder repeating its own answer (its response was lost) is told it is recorded.
			same, err := completed(ctx, t, id, lease, key)
			if err != nil {
				return err
			}
			if same {
				return errRepeat
			}
			return ErrLeaseLost
		}
		_, err = t.ExecContext(ctx, "UPDATE contents SET reply = ? WHERE seq = (SELECT seq FROM messages WHERE id = ?)", reply, id)
		if err != nil || len(files) == 0 {
			return err
		}
		var seq int64
		if err := t.QueryRowContext(ctx, "SELECT seq FROM messages WHERE id = ?", id).Scan(&seq); err != nil {
			return err
		}
		return s.putFiles(ctx, t, seq, true, files)
	})
	if errors.Is(err, errRepeat) {
		return nil // nothing was written: the first call recorded it
	}
	if err == nil {
		s.afterWrite(ctx, int64(len(reply))+filesSize(files))
	}
	return err
}

// Release gives a running message back: queued again for another attempt, or failed with reason
// when it has had maxAttempts.
func (s *Store) Release(ctx context.Context, id, lease, code, reason string, maxAttempts int) (State, error) {
	now := ms(time.Now())
	var state State
	err := s.tx(ctx, func(t *sql.Tx) error {
		var attempts int
		err := t.QueryRowContext(ctx, "SELECT attempts FROM messages WHERE id = ? AND state = 'running' AND lease_id = ? AND lease_id <> ''", id, lease).Scan(&attempts)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		if err != nil {
			return err
		}
		if attempts >= maxAttempts {
			state = Failed
			_, err = t.ExecContext(ctx, `UPDATE messages SET state = 'failed', error = ?, error_code = ?, lease_id = '', lease_until = 0, updated_at = ?, finished_at = ?
				WHERE id = ?`, fmt.Sprintf("%s (after %d attempts)", reason, attempts), code, now, now, id)
			return err
		}
		state = Queued
		_, err = t.ExecContext(ctx, `UPDATE messages SET state = 'queued', error = ?, error_code = ?, lease_id = '', lease_until = 0, updated_at = ? WHERE id = ?`,
			reason, code, now, id)
		return err
	})
	return state, err
}

// Extend makes a lease last d from now.
func (s *Store) Extend(ctx context.Context, id, lease string, d time.Duration) error {
	res, err := s.w.ExecContext(ctx, "UPDATE messages SET lease_until = ? WHERE id = ? AND state = 'running' AND lease_id = ? AND lease_id <> ''",
		ms(time.Now().Add(d)), id, lease)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// ReapLeases puts running messages whose lease ran out before now back in the queue, or fails
// those that have had maxAttempts. It returns how many it changed.
func (s *Store) ReapLeases(ctx context.Context, now time.Time, maxAttempts int) (int, error) {
	t := ms(now)
	var n int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE messages SET state = 'failed', error = 'lease lost after ' || attempts || ' attempts', error_code = 'lease_lost',
			lease_id = '', lease_until = 0, updated_at = ?, finished_at = ? WHERE state = 'running' AND lease_id <> '' AND lease_until < ? AND attempts >= ?`, t, t, t, maxAttempts)
		if err != nil {
			return err
		}
		a, _ := res.RowsAffected()
		res, err = tx.ExecContext(ctx, `UPDATE messages SET state = 'queued', error = 'lease lost', error_code = 'lease_lost', lease_id = '', lease_until = 0, updated_at = ?
			WHERE state = 'running' AND lease_id <> '' AND lease_until < ?`, t, t)
		if err != nil {
			return err
		}
		b, _ := res.RowsAffected()
		n = a + b
		return nil
	})
	return int(n), err
}

// RequeueRunning puts every running message for these recipients back in the queue: a node does
// this at start for the agents it runs itself, whose turns ended with the previous process.
func (s *Store) RequeueRunning(ctx context.Context, recipients []string, maxAttempts int) (int, error) {
	if len(recipients) == 0 {
		return 0, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(recipients)), ",")
	now := ms(time.Now())
	var total int64
	err := s.tx(ctx, func(t *sql.Tx) error {
		// The last allowed attempt was the one interrupted: the message fails, as a lost lease would.
		args := []any{now, now}
		for _, r := range recipients {
			args = append(args, r)
		}
		args = append(args, maxAttempts)
		res, err := t.ExecContext(ctx, `UPDATE messages SET state = 'failed', error = 'interrupted by a node restart after ' || attempts || ' attempts', error_code = 'node_restart',
			lease_id = '', lease_until = 0, updated_at = ?, finished_at = ? WHERE state = 'running' AND recipient IN (`+ph+`) AND attempts >= ?`, args...)
		if err != nil {
			return err
		}
		a, _ := res.RowsAffected()
		args = []any{now}
		for _, r := range recipients {
			args = append(args, r)
		}
		res, err = t.ExecContext(ctx, `UPDATE messages SET state = 'queued', error = 'interrupted: the node restarted', lease_id = '', lease_until = 0,
			updated_at = ? WHERE state = 'running' AND recipient IN (`+ph+`)`, args...)
		if err != nil {
			return err
		}
		b, _ := res.RowsAffected()
		total = a + b
		return nil
	})
	return int(total), err
}

// Expire ends queued messages whose time ran out before now.
func (s *Store) Expire(ctx context.Context, now time.Time) (int, error) {
	t := ms(now)
	res, err := s.w.ExecContext(ctx, `UPDATE messages SET state = 'expired', error = 'expired before an agent took it', error_code = 'expired', updated_at = ?, finished_at = ?
		WHERE state = 'queued' AND expires_at > 0 AND expires_at <= ? AND origin <> 'remote'`, t, t, t)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Cancel ends a message that is not final yet.
func (s *Store) Cancel(ctx context.Context, id string) (Message, error) {
	now := ms(time.Now())
	var m Message
	err := s.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE messages SET state = 'cancelled', lease_id = '', lease_until = 0, updated_at = ?, finished_at = ?
			WHERE id = ? AND state IN ('queued', 'running')`, now, now, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		m, err = s.get(ctx, t, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrFinal
		}
		return nil
	})
	return m, err
}

// EditQueued changes the body of a message no one has taken yet (queued again after a lease ran
// out is taken: a worker had its body); ErrFinal (with the message as it is) otherwise.
func (s *Store) EditQueued(ctx context.Context, id, body string) (Message, error) {
	var m Message
	err := s.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE messages SET body_size = ?, updated_at = ? WHERE id = ? AND state = 'queued' AND attempts = 0`, len(body), ms(time.Now()), id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 1 {
			if _, err := t.ExecContext(ctx, `UPDATE contents SET body = ? WHERE seq = (SELECT seq FROM messages WHERE id = ?)`, body, id); err != nil {
				return err
			}
		}
		if m, err = s.get(ctx, t, id); err != nil {
			return err
		}
		if n == 0 {
			return ErrFinal
		}
		return nil
	})
	return m, err
}

// Counts says how many messages for recipient are queued and running.
func (s *Store) Counts(ctx context.Context, recipient string) (queued, running int, err error) {
	err = s.r.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM messages INDEXED BY messages_queue WHERE recipient = ?1 AND state = 'queued'),
		(SELECT count(*) FROM messages INDEXED BY messages_queue WHERE recipient = ?1 AND state = 'running')`, recipient).Scan(&queued, &running)
	return
}

// AddEvent records an event of a message. Past MaxEventsPerMessage events are dropped (the last
// one kept says so); data longer than MaxEventData is cut.
func (s *Store) AddEvent(ctx context.Context, id, kind, data string) error {
	return s.addEvent(ctx, id, kind, data, false)
}

// AddTurnEvent records an event of a running turn: once the message is final (cancelled while the
// turn still ran) it is dropped, so whoever read the events of a final message read them all.
func (s *Store) AddTurnEvent(ctx context.Context, id, kind, data string) error {
	return s.addEvent(ctx, id, kind, data, true)
}

func (s *Store) addEvent(ctx context.Context, id, kind, data string, live bool) error {
	return s.tx(ctx, func(t *sql.Tx) error {
		var seq int64
		var n int
		var state State
		err := t.QueryRowContext(ctx, "SELECT seq, events, state FROM messages WHERE id = ?", id).Scan(&seq, &n, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || (live && state.Final()) {
			return err
		}
		_, err = s.insertEvent(ctx, t, id, seq, n, kind, data)
		return err
	})
}

// insertEvent adds an event after the n a message has, within MaxEventsPerMessage (the last one
// kept says the rest was dropped); it returns how many the message has then.
//
// A "usage" event is counted (usage.go) whether or not it is kept among the message's events.
func (s *Store) insertEvent(ctx context.Context, t *sql.Tx, id string, seq int64, n int, kind, data string) (int, error) {
	now := time.Now()
	if len(data) > s.opt.MaxEventData {
		data = data[:s.opt.MaxEventData] + truncMark
	}
	var u Usage
	usage := false
	if kind == "usage" {
		// Not readable (or cut to MaxEventData): counted nowhere, and kept as usage it would make
		// every relay of the message's events refused; what the agent did is said by a notice.
		if u, usage = readUsage(data); !usage {
			kind, data = "notice", unreadableUsage
		}
	}
	switch {
	case usage:
		// Counted, and kept past the limit (at most maxUsageEvents), so it reaches the other nodes.
		var have, counted int
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE msg = ? AND kind = 'usage'", seq).Scan(&have); err != nil {
			return n, err
		}
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM turn_usage WHERE turn = ?", u.Turn).Scan(&counted); err != nil {
			return n, err
		}
		if counted > 0 || have >= maxUsageEvents {
			return n, nil // a turn counted already (its event come again) takes no place
		}
		if err := addUsage(ctx, t, id, u, now); err != nil {
			return n, err
		}
	case n >= s.opt.MaxEventsPerMessage:
		// Usage took the last place (it is kept past the limit): the first event dropped gives its
		// place to the marker. One event for one, so a copy's count never runs ahead of the events
		// it took (relayevents.go resumes from it).
		var marked int
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE msg = ? AND kind = 'truncated'", seq).Scan(&marked); err != nil || marked > 0 {
			return n, err
		}
		kind, data = "truncated", truncatedEvents
	case n == s.opt.MaxEventsPerMessage-1:
		kind, data = "truncated", truncatedEvents
	}
	if _, err := t.ExecContext(ctx, "INSERT INTO events (msg, seq, ts, kind, data) VALUES (?, ?, ?, ?, ?)", seq, n+1, ms(now), kind, data); err != nil {
		return n, err
	}
	_, err := t.ExecContext(ctx, "UPDATE messages SET events = events + 1 WHERE seq = ?", seq)
	return n + 1, err
}

// RelayedEvent is an event the node running a message's agent hands over: its kind and data.
type RelayedEvent struct {
	Kind string `json:"kind"`
	Data string `json:"data"`
}

// RelayEvents takes, for a running message held under lease, the events its agent's node recorded
// after its event number after; it returns how many of that node's events the message has taken.
// What was taken before is skipped (a retry, a new delivery from the start), and a batch that
// would leave a gap is not taken: the caller sends again from the number returned.
func (s *Store) RelayEvents(ctx context.Context, id, lease string, after int64, evs []RelayedEvent) (int64, error) {
	var relayed int64
	err := s.tx(ctx, func(t *sql.Tx) error {
		var seq int64
		var n int
		var state State
		var held string
		err := t.QueryRowContext(ctx, "SELECT seq, events, state, lease_id, relayed FROM messages WHERE id = ?", id).Scan(&seq, &n, &state, &held, &relayed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if lease == "" || held != lease || state != Running {
			return ErrLeaseLost
		}
		if after > relayed || after+int64(len(evs)) <= relayed {
			return nil // a gap, or nothing new: the caller goes on from relayed
		}
		for _, e := range evs[relayed-after:] {
			if n, err = s.insertEvent(ctx, t, id, seq, n, e.Kind, e.Data); err != nil {
				return err
			}
		}
		relayed = after + int64(len(evs))
		_, err = t.ExecContext(ctx, "UPDATE messages SET relayed = ? WHERE seq = ?", relayed, seq)
		return err
	})
	return relayed, err
}

// EventCount is how many events a message has.
func (s *Store) EventCount(ctx context.Context, id string) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, "SELECT events FROM messages WHERE id = ?", id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return n, err
}

// Events returns up to limit events of a message after the event numbered after.
func (s *Store) Events(ctx context.Context, id string, after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	rows, err := s.r.QueryContext(ctx, `SELECT e.seq, e.ts, e.kind, e.data FROM events e JOIN messages m ON m.seq = e.msg
		WHERE m.id = ? AND e.seq > ? ORDER BY e.seq LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		if err := rows.Scan(&e.Seq, &ts, &e.Kind, &e.Data); err != nil {
			return nil, err
		}
		e.Time = fromMS(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Session is the ACP session id an agent keeps for a key ("" for none).
func (s *Store) Session(ctx context.Context, agent, key string) (string, error) {
	var id string
	err := s.r.QueryRowContext(ctx, "SELECT session_id FROM sessions WHERE agent = ? AND key = ?", agent, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// PutSession records the ACP session an agent keeps for a key.
func (s *Store) PutSession(ctx context.Context, agent, key, sessionID string) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO sessions (agent, key, session_id, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (agent, key) DO UPDATE SET session_id = excluded.session_id, updated_at = excluded.updated_at`,
		agent, key, sessionID, ms(time.Now()))
	return err
}

const gcBatch = 500

// GC deletes final messages that finished before before (with their bodies and events) and
// sessions unused since then. It works in small batches, so writers are not held up for long.
func (s *Store) GC(ctx context.Context, before time.Time) (int, error) {
	t := ms(before)
	total := 0
	for {
		var n int64
		err := s.tx(ctx, func(tx *sql.Tx) error {
			sel := `SELECT seq FROM messages INDEXED BY messages_state WHERE state IN ('done','failed','cancelled','expired') AND finished_at < ? LIMIT ?`
			if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE msg IN ("+sel+")", t, gcBatch); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM contents WHERE seq IN ("+sel+")", t, gcBatch); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM files WHERE msg IN ("+sel+")", t, gcBatch); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM turn_changes WHERE msg IN (SELECT id FROM messages WHERE seq IN ("+sel+"))", t, gcBatch); err != nil {
				return err
			}
			// Content that no file refers to any more: of these messages, or of reply files an
			// earlier attempt or a settled copy dropped.
			if err := dropOrphans(ctx, tx); err != nil {
				return err
			}
			if err := gcUsage(ctx, tx, time.Now()); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE seq IN ("+sel+")", t, gcBatch)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			return nil
		})
		if err != nil {
			return total, err
		}
		total += int(n)
		if n < gcBatch {
			break
		}
	}
	_, err := s.w.ExecContext(ctx, "DELETE FROM sessions WHERE updated_at < ?", t)
	return total, err
}

// Maintain gives free pages back to the file system and folds the WAL into the database,
// truncating it. A node runs it periodically; it is cheap when there is nothing to do.
func (s *Store) Maintain(ctx context.Context) error {
	if s.path == "" {
		return nil
	}
	s.maintainMu.Lock()
	defer s.maintainMu.Unlock()
	var free int64
	if err := s.w.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
		return err
	}
	// In steps, so a large free list does not hold the write lock for long.
	for free > 0 {
		step := min(free, 2048)
		if _, err := s.w.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", step)); err != nil {
			return err
		}
		free -= step
	}
	var busy, logPages, done int
	if err := s.w.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &done); err != nil {
		return err
	}
	return nil
}

// IntegrityCheck runs SQLite's integrity check.
func (s *Store) IntegrityCheck(ctx context.Context) error {
	var res string
	if err := s.w.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("integrity check: %s", res)
	}
	return nil
}

// Live is how many bytes of the store hold data.
func (s *Store) Live(ctx context.Context) (int64, error) { return liveBytes(ctx, s.r) }

// Party is who a token-holding client is: what it sent, and what went to the agents it acts as.
type Party struct {
	Sender     string
	Recipients []string
}

func (p *Party) where(prefix string) (string, []any) {
	cond := prefix + "sender = ?"
	args := []any{p.Sender}
	if len(p.Recipients) > 0 {
		cond += " OR " + prefix + "recipient IN (" + strings.TrimSuffix(strings.Repeat("?,", len(p.Recipients)), ",") + ")"
		for _, r := range p.Recipients {
			args = append(args, r)
		}
	}
	return "(" + cond + ")", args
}

const (
	truncatedEvents = "later events were not kept (limits.max_events_per_message)"
	unreadableUsage = `{"severity":"warning","text":"the agent's usage of this turn could not be read: not counted"}`
)

// checkpointWait bounds how long afterWrite's checkpoint waits for readers (see there).
const checkpointWait = 250 * time.Millisecond

// shortWait runs fn on c with busy_timeout at d, and puts the connection's own wait back after,
// whatever ctx has become. The restore is deferred before the change: SQLite applies the PRAGMA
// while preparing it, so a ctx cancelled after that still errors, and the connection would go
// back to the pool giving up on readers after d (a later Maintain would leave the WAL as it is).
func shortWait(ctx context.Context, c *sql.Conn, d time.Duration, fn func()) {
	defer func() { _, _ = c.ExecContext(context.WithoutCancel(ctx), "PRAGMA busy_timeout = 10000") }()
	if _, err := c.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", d.Milliseconds())); err != nil {
		return
	}
	fn()
}

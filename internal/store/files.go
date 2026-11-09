package store

// Files attached to a message or to its reply. They live in their own tables: a message's row and
// its body stay small however much is attached, and the data is read only when asked for.
//
// A file is either content or a link. Content is kept once per SHA-256 (blobs): the same file
// attached to many messages, or handed back in a reply, takes its bytes once. A link (uri) is a
// file kept elsewhere, a path or a URL: nothing of it is copied.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// File is a file attached to a message (Reply false) or to its reply. Data is set only where it
// is asked for (FilesOf); a message lists its files without it. A link has URI and no data; its
// Size is what its sender said, if anything.
type File struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int    `json:"size"`
	Reply  bool   `json:"reply,omitempty"`
	URI    string `json:"uri,omitempty"`
	SHA256 string `json:"sha256,omitempty"` // of the content (set by the store, not taken from senders)
	Data   []byte `json:"data,omitempty"`
}

// Link says whether the file is a link to a file kept elsewhere.
func (f File) Link() bool { return f.URI != "" }

// Hash is the SHA-256 of data, as hex.
func Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// MaxFilesPerRequest bounds the files one request may carry, whatever the limits say: a list is
// refused while it is decoded, before a long one is held in memory.
// A listing of both sides of a message holds up to twice limits.max_files (at most 256).
const MaxFilesPerRequest = 512

// Files is a list of files that refuses, while decoding, more than MaxFilesPerRequest entries.
type Files []File

// ErrBadFiles is a list of files that does not decode: its content is wrong, not its transport.
var ErrBadFiles = errors.New("bad list of files")

func (fs *Files) UnmarshalJSON(b []byte) error {
	if err := fs.decode(b, true); err != nil {
		return fmt.Errorf("%w: %v", ErrBadFiles, err)
	}
	return nil
}

func (fs *Files) decode(b []byte, strict bool) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*fs = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return errors.New("files: a list")
	}
	var out []File
	for dec.More() {
		if len(out) == MaxFilesPerRequest {
			return fmt.Errorf("files: more than %d", MaxFilesPerRequest)
		}
		var f File
		// Strict like the API's own decoding: a misspelt field (data_base64) is an error, not an empty file.
		if strict {
			dec.DisallowUnknownFields()
		}
		if err := dec.Decode(&f); err != nil {
			return err
		}
		out = append(out, f)
	}
	*fs = out
	return nil
}

// FileLimits bound the files of one side of a message.
type FileLimits struct {
	Count int
	Size  int64
}

// ErrFileLimits is returned when files would take a side over its FileLimits.
var ErrFileLimits = errors.New("over the file limits")

const filesSchema = `
CREATE TABLE IF NOT EXISTS blobs (
	id   INTEGER PRIMARY KEY,
	hash TEXT NOT NULL UNIQUE,
	data BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS files (
	id    INTEGER PRIMARY KEY,
	msg   INTEGER NOT NULL,
	name  TEXT NOT NULL,
	reply INTEGER NOT NULL DEFAULT 0,
	type  TEXT NOT NULL,
	size  INTEGER NOT NULL,
	hash  TEXT NOT NULL DEFAULT '',
	uri   TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS files_msg ON files (msg, reply, name);
CREATE INDEX IF NOT EXISTS files_hash ON files (hash) WHERE hash <> '';
`

// migrateFiles moves the first release's files (data in each row) to blobs, once per content.
func migrateFiles(db *sql.DB) error {
	var n int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('files') WHERE name = 'data'").Scan(&n); err != nil || n == 0 {
		return err
	}
	t, err := db.Begin()
	if err != nil {
		return err
	}
	defer t.Rollback()
	if _, err := t.Exec("ALTER TABLE files RENAME TO files_v1"); err != nil {
		return err
	}
	if _, err := t.Exec("DROP INDEX IF EXISTS files_msg"); err != nil {
		return err
	}
	if _, err := t.Exec(filesSchema); err != nil {
		return err
	}
	// Row by row, one in memory at a time: a store of any size migrates in bounded memory.
	for last := int64(-1); ; {
		var id, msg int64
		var name, typ string
		var reply bool
		var data []byte
		err := t.QueryRow("SELECT id, msg, name, reply, type, data FROM files_v1 WHERE id > ? ORDER BY id LIMIT 1", last).
			Scan(&id, &msg, &name, &reply, &typ, &data)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		last = id
		h := Hash(data)
		if _, err := t.Exec("INSERT INTO blobs (hash, data) VALUES (?, ?) ON CONFLICT (hash) DO NOTHING", h, nonNil(data)); err != nil {
			return err
		}
		if _, err := t.Exec("INSERT INTO files (id, msg, name, reply, type, size, hash) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id, msg, name, reply, typ, len(data), h); err != nil {
			return err
		}
	}
	if _, err := t.Exec("DROP TABLE files_v1"); err != nil {
		return err
	}
	return t.Commit()
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{} // an empty file is an empty blob, not NULL
	}
	return b
}

// ErrFileExists is returned when a message (or its reply) already has a file of that name.
var ErrFileExists = errors.New("a file of that name is already attached")

// filesSize is the bytes of content files carry (links carry none).
func filesSize(files []File) int64 {
	var n int64
	for _, f := range files {
		if !f.Link() {
			n += int64(len(f.Data))
		}
	}
	return n
}

// putFiles stores files for the message with row seq. Content already in the store (the same
// SHA-256) is not written again; only new content counts against max_store.
func (s *Store) putFiles(ctx context.Context, t *sql.Tx, seq int64, reply bool, files []File) error {
	var fresh int64
	counted := map[string]bool{}
	for _, f := range files {
		if f.Link() {
			continue
		}
		h := Hash(f.Data)
		if counted[h] {
			continue // the same new content twice in this request is stored once
		}
		counted[h] = true
		var known int
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM blobs WHERE hash = ?", h).Scan(&known); err != nil {
			return err
		}
		if known == 0 {
			fresh += int64(len(f.Data))
		}
	}
	if err := s.roomFor(ctx, t, fresh); err != nil {
		return err
	}
	for _, f := range files {
		hash, size := "", f.Size
		if !f.Link() {
			hash, size = Hash(f.Data), len(f.Data)
			if _, err := t.ExecContext(ctx, "INSERT INTO blobs (hash, data) VALUES (?, ?) ON CONFLICT (hash) DO NOTHING", hash, nonNil(f.Data)); err != nil {
				return err
			}
		}
		_, err := t.ExecContext(ctx, "INSERT INTO files (msg, name, reply, type, size, hash, uri) VALUES (?, ?, ?, ?, ?, ?, ?)",
			seq, f.Name, reply, f.Type, size, hash, f.URI)
		if err != nil {
			var n int
			if t.QueryRowContext(ctx, "SELECT count(*) FROM files WHERE msg = ? AND reply = ? AND name = ?", seq, reply, f.Name).Scan(&n) == nil && n > 0 {
				return fmt.Errorf("%w: %s", ErrFileExists, f.Name)
			}
			return err
		}
	}
	return nil
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// listFiles lists a message's files, with their data when data is set (links have none).
func listFiles(ctx context.Context, q querier, id string, data bool) ([]File, error) {
	col := "NULL"
	if data {
		col = "b.data"
	}
	rows, err := q.QueryContext(ctx, `SELECT f.name, f.reply, f.type, f.size, f.uri, f.hash, `+col+` FROM files f
		JOIN messages m ON m.seq = f.msg LEFT JOIN blobs b ON b.hash = f.hash AND f.hash <> ''
		WHERE m.id = ? ORDER BY f.reply, f.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Name, &f.Reply, &f.Type, &f.Size, &f.URI, &f.SHA256, &f.Data); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FilesOf returns a message's files with their data.
func (s *Store) FilesOf(ctx context.Context, id string) ([]File, error) {
	return listFiles(ctx, s.r, id, true)
}

// AttachReply adds files to the reply of a message running under lease (an agent attaching to its
// answer while its turn runs), within lim counted with what is attached already. The attempt's
// files go if the message is handed back for another attempt.
func (s *Store) AttachReply(ctx context.Context, id, lease string, files []File, lim FileLimits) error {
	return s.tx(ctx, func(t *sql.Tx) error {
		var seq int64
		err := t.QueryRowContext(ctx, "SELECT seq FROM messages WHERE id = ? AND state = 'running' AND lease_id = ? AND lease_id <> ''", id, lease).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		if err != nil {
			return err
		}
		if files, err = notAttached(ctx, t, seq, files); err != nil || len(files) == 0 {
			return err // all of them attached already: nothing to weigh against the limits
		}
		var count int
		var size int64
		if err := t.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(CASE WHEN uri = '' THEN size ELSE 0 END), 0) FROM files WHERE msg = ? AND reply = 1", seq).Scan(&count, &size); err != nil {
			return err
		}
		if count+len(files) > lim.Count || size+filesSize(files) > lim.Size {
			return fmt.Errorf("%w: the reply would have %d files, %d bytes (limits.max_files %d, max_files_size %d)",
				ErrFileLimits, count+len(files), size+filesSize(files), lim.Count, lim.Size)
		}
		return s.putFiles(ctx, t, seq, true, files)
	})
}

// HasContent says whether the store keeps content with this SHA-256.
func (s *Store) HasContent(ctx context.Context, hash string) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, "SELECT count(*) FROM blobs WHERE hash = ?", hash).Scan(&n)
	return n > 0, err
}

// dropOrphans deletes content no file refers to any more.
func dropOrphans(ctx context.Context, t *sql.Tx) error {
	_, err := t.ExecContext(ctx, orphanBlobs)
	return err
}

// orphanBlobs repeats the partial index's condition (a non-empty hash), or SQLite cannot use
// files_hash and scans every file for every blob on each collection.
const orphanBlobs = "DELETE FROM blobs WHERE NOT EXISTS (SELECT 1 FROM files f WHERE f.hash = blobs.hash AND f.hash <> '')"

// ReplyFiles counts the files and bytes attached to a message's reply so far.
func (s *Store) ReplyFiles(ctx context.Context, id string) (count int, size int64, err error) {
	err = s.r.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(CASE WHEN f.uri = '' THEN f.size ELSE 0 END), 0) FROM files f JOIN messages m ON m.seq = f.msg
		WHERE m.id = ? AND f.reply = 1`, id).Scan(&count, &size)
	return count, size, err
}

// roomFor refuses (ErrStoreFull) n more bytes above limits.max_store.
func (s *Store) roomFor(ctx context.Context, t *sql.Tx, n int64) error {
	if s.opt.MaxStore <= 0 {
		return nil
	}
	live, err := liveBytes(ctx, t)
	if err != nil {
		return err
	}
	if live+n > s.opt.MaxStore {
		return ErrStoreFull
	}
	return nil
}

// notAttached leaves out the files a reply has already, by name and content: the repeat of an
// attach whose response was lost is no error. The same name with other content still is.
func notAttached(ctx context.Context, t *sql.Tx, seq int64, files []File) ([]File, error) {
	out := files[:0:0]
	for _, f := range files {
		hash, size := "", f.Size // a link's size is as given
		if !f.Link() {
			hash, size = Hash(f.Data), len(f.Data)
		}
		var n int
		if err := t.QueryRowContext(ctx, "SELECT count(*) FROM files WHERE msg = ? AND reply = 1 AND name = ? AND type = ? AND size = ? AND hash = ? AND uri = ?",
			seq, f.Name, f.Type, size, hash, f.URI).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, f)
		}
	}
	return out, nil
}

// DecodeTolerant decodes a list of files as a response from another node carries it: a field this
// version does not know (sent by a newer one) is ignored, not refused, as decoding a user's
// request refuses it (UnmarshalJSON). The bound on the files holds the same.
func (fs *Files) DecodeTolerant(b []byte) error {
	if err := fs.decode(b, false); err != nil {
		return fmt.Errorf("%w: %v", ErrBadFiles, err)
	}
	return nil
}

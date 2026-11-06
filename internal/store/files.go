package store

// Files attached to a message or to its reply. They live in their own table: a message's row and
// its body stay small however much is attached, and the data is read only when asked for.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// File is a file attached to a message (Reply false) or to its reply. Data is set only where it
// is asked for (FilesOf, File); a message lists its files without it.
type File struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  int    `json:"size"`
	Reply bool   `json:"reply,omitempty"`
	Data  []byte `json:"data,omitempty"`
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
	if err := fs.decode(b); err != nil {
		return fmt.Errorf("%w: %v", ErrBadFiles, err)
	}
	return nil
}

func (fs *Files) decode(b []byte) error {
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
		dec.DisallowUnknownFields()
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
CREATE TABLE IF NOT EXISTS files (
	id    INTEGER PRIMARY KEY,
	msg   INTEGER NOT NULL,
	name  TEXT NOT NULL,
	reply INTEGER NOT NULL DEFAULT 0,
	type  TEXT NOT NULL,
	size  INTEGER NOT NULL,
	data  BLOB NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS files_msg ON files (msg, reply, name);
`

// ErrFileExists is returned when a message (or its reply) already has a file of that name.
var ErrFileExists = errors.New("a file of that name is already attached")

func filesSize(files []File) int64 {
	var n int64
	for _, f := range files {
		n += int64(len(f.Data))
	}
	return n
}

// putFiles stores files for the message with row seq.
func putFiles(ctx context.Context, t *sql.Tx, seq int64, reply bool, files []File) error {
	for _, f := range files {
		data := f.Data
		if data == nil {
			data = []byte{} // an empty file is an empty blob, not NULL
		}
		_, err := t.ExecContext(ctx, "INSERT INTO files (msg, name, reply, type, size, data) VALUES (?, ?, ?, ?, ?, ?)",
			seq, f.Name, reply, f.Type, len(data), data)
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

// listFiles lists a message's files, with their data when data is set.
func listFiles(ctx context.Context, q querier, id string, data bool) ([]File, error) {
	col := "NULL"
	if data {
		col = "f.data"
	}
	rows, err := q.QueryContext(ctx, `SELECT f.name, f.reply, f.type, f.size, `+col+` FROM files f
		JOIN messages m ON m.seq = f.msg WHERE m.id = ? ORDER BY f.reply, f.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Name, &f.Reply, &f.Type, &f.Size, &f.Data); err != nil {
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
		var count int
		var size int64
		if err := t.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(size), 0) FROM files WHERE msg = ? AND reply = 1", seq).Scan(&count, &size); err != nil {
			return err
		}
		if count+len(files) > lim.Count || size+filesSize(files) > lim.Size {
			return fmt.Errorf("%w: the reply would have %d files, %d bytes (limits.max_files %d, max_files_size %d)",
				ErrFileLimits, count+len(files), size+filesSize(files), lim.Count, lim.Size)
		}
		if err := s.roomFor(ctx, t, filesSize(files)); err != nil {
			return err
		}
		return putFiles(ctx, t, seq, true, files)
	})
}

// ReplyFiles counts the files and bytes attached to a message's reply so far.
func (s *Store) ReplyFiles(ctx context.Context, id string) (count int, size int64, err error) {
	err = s.r.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(f.size), 0) FROM files f JOIN messages m ON m.seq = f.msg
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

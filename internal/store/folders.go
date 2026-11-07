package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Folders the node's user sorts chats into, in the app: a chat (an acpost thread, an agent's ACP
// session, a cloud sandbox) is in one folder or none. A folder is the app's, not a directory.

const foldersSchema = `
CREATE TABLE IF NOT EXISTS folders (
	id   TEXT    PRIMARY KEY,
	name TEXT    NOT NULL,
	pos  INTEGER NOT NULL DEFAULT 0,
	ts   INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS chat_folders (
	chat   TEXT PRIMARY KEY,
	folder TEXT NOT NULL REFERENCES folders (id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS chat_folders_folder ON chat_folders (folder);
`

// Folder is a folder of chats.
type Folder struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Pos   int      `json:"pos"`
	Chats []string `json:"chats"` // the chats in it
}

// MaxFolders bounds how many folders there are; MaxChatKey how long a chat's key is.
const (
	MaxFolders  = 500
	MaxChatKey  = 512
	maxFolderNm = 120
)

// ErrNoFolder: no such folder.
var ErrNoFolder = errors.New("no such folder")

func folderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxFolderNm || strings.ContainsFunc(name, func(c rune) bool { return c < ' ' || c == 0x7f }) {
		return "", fmt.Errorf("a folder's name: 1 to %d printable characters", maxFolderNm)
	}
	return name, nil
}

// Folders lists the folders, in their order, with their chats.
func (s *Store) Folders(ctx context.Context) ([]Folder, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, name, pos FROM folders ORDER BY pos, ts`)
	if err != nil {
		return nil, err
	}
	var out []Folder
	idx := map[string]int{}
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.Pos); err != nil {
			rows.Close()
			return nil, err
		}
		f.Chats = []string{}
		idx[f.ID] = len(out)
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	crows, err := s.r.QueryContext(ctx, `SELECT chat, folder FROM chat_folders ORDER BY chat`)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var chat, folder string
		if err := crows.Scan(&chat, &folder); err != nil {
			return nil, err
		}
		if i, ok := idx[folder]; ok {
			out[i].Chats = append(out[i].Chats, chat)
		}
	}
	if out == nil {
		out = []Folder{}
	}
	return out, crows.Err()
}

// CreateFolder makes a folder, last in the order.
func (s *Store) CreateFolder(ctx context.Context, name string) (Folder, error) {
	name, err := folderName(name)
	if err != nil {
		return Folder{}, err
	}
	f := Folder{ID: "fld_" + NewID()[4:], Name: name, Chats: []string{}}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(max(pos), -1) + 1 FROM folders`).Scan(&n, &f.Pos); err != nil {
			return err
		}
		if n >= MaxFolders {
			return fmt.Errorf("%d folders already: the most there may be", n)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO folders (id, name, pos, ts) VALUES (?, ?, ?, ?)`, f.ID, f.Name, f.Pos, time.Now().UnixMilli())
		return err
	})
	return f, err
}

// UpdateFolder renames a folder and/or moves it in the order (pos < 0: where it is).
func (s *Store) UpdateFolder(ctx context.Context, id, name string, pos int) error {
	if name != "" {
		var err error
		if name, err = folderName(name); err != nil {
			return err
		}
	}
	res, err := s.w.ExecContext(ctx, `UPDATE folders SET name = CASE WHEN ? = '' THEN name ELSE ? END,
		pos = CASE WHEN ? < 0 THEN pos ELSE ? END WHERE id = ?`, name, name, pos, pos, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNoFolder
	}
	return nil
}

// DeleteFolder removes a folder; its chats are in none after it.
func (s *Store) DeleteFolder(ctx context.Context, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_folders WHERE folder = ?`, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return ErrNoFolder
		}
		return nil
	})
}

// MoveChat puts a chat in a folder ("" takes it out of any).
func (s *Store) MoveChat(ctx context.Context, chat, folder string) error {
	if chat == "" || len(chat) > MaxChatKey || strings.ContainsFunc(chat, func(c rune) bool { return c < ' ' || c == 0x7f }) {
		return fmt.Errorf("a chat's key: 1 to %d printable characters", MaxChatKey)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if folder == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM chat_folders WHERE chat = ?`, chat)
			return err
		}
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM folders WHERE id = ?`, folder).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoFolder
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_folders (chat, folder) VALUES (?, ?)
			ON CONFLICT (chat) DO UPDATE SET folder = excluded.folder`, chat, folder)
		return err
	})
}

// ActiveRecipients lists the recipients with a message queued or running whose address starts so.
func (s *Store) ActiveRecipients(ctx context.Context, prefix string) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT DISTINCT recipient FROM messages
		WHERE state IN ('queued', 'running') AND substr(recipient, 1, length(?1)) = ?1`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

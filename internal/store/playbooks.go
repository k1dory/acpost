package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Playbooks: prompts the node's user keeps for tasks done again (the app's / menu); and the names
// the user gives chats.

const playbooksSchema = `
CREATE TABLE IF NOT EXISTS playbooks (
	name TEXT    PRIMARY KEY,
	body TEXT    NOT NULL,
	ts   INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS chat_titles (
	chat  TEXT PRIMARY KEY,
	title TEXT NOT NULL
) WITHOUT ROWID;
`

// Playbook is a kept prompt.
type Playbook struct {
	Name    string    `json:"name"`
	Body    string    `json:"body"`
	Updated time.Time `json:"updated"`
}

var playbookName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ErrNoPlaybook: no such playbook.
var ErrNoPlaybook = errors.New("no such playbook")

// MaxPlaybook bounds a playbook's text.
const MaxPlaybook = 64 << 10

func (s *Store) Playbooks(ctx context.Context) ([]Playbook, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT name, body, ts FROM playbooks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Playbook{}
	for rows.Next() {
		var p Playbook
		var ts int64
		if err := rows.Scan(&p.Name, &p.Body, &ts); err != nil {
			return nil, err
		}
		p.Updated = time.UnixMilli(ts).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PutPlaybook(ctx context.Context, p Playbook) error {
	if !playbookName.MatchString(p.Name) {
		return fmt.Errorf("a playbook's name: letters, digits, . _ - (up to 64), as /name calls it")
	}
	if strings.TrimSpace(p.Body) == "" || len(p.Body) > MaxPlaybook {
		return fmt.Errorf("a playbook's text: 1 byte to %d KiB", MaxPlaybook>>10)
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO playbooks (name, body, ts) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET body = excluded.body, ts = excluded.ts`, p.Name, p.Body, time.Now().UnixMilli())
	return err
}

func (s *Store) DeletePlaybook(ctx context.Context, name string) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM playbooks WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNoPlaybook
	}
	return nil
}

// ChatTitles are the names the user gave chats, by chat key.
func (s *Store) ChatTitles(ctx context.Context) (map[string]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT chat, title FROM chat_titles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, t string
		if err := rows.Scan(&c, &t); err != nil {
			return nil, err
		}
		out[c] = t
	}
	return out, rows.Err()
}

// SetChatTitle names a chat ("" gives it back its own title).
func (s *Store) SetChatTitle(ctx context.Context, chat, title string) error {
	title = strings.TrimSpace(title)
	if chat == "" || len(chat) > MaxChatKey || len(title) > 200 || strings.ContainsFunc(title, func(r rune) bool { return r < ' ' }) {
		return fmt.Errorf("a chat's title: up to 200 printable characters")
	}
	if title == "" {
		_, err := s.w.ExecContext(ctx, `DELETE FROM chat_titles WHERE chat = ?`, chat)
		return err
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO chat_titles (chat, title) VALUES (?, ?)
		ON CONFLICT (chat) DO UPDATE SET title = excluded.title`, chat, title)
	return err
}

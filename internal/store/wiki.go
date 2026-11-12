package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// The wiki: for a repository (a directory), pages an agent wrote after reading it — an overview,
// the architecture, the modules, how to build and run — kept on the node, made again on request.

const wikiSchema = `
CREATE TABLE IF NOT EXISTS wikis (
	repo    TEXT    PRIMARY KEY, -- the directory
	agent   TEXT    NOT NULL,
	lang    TEXT    NOT NULL DEFAULT '',
	status  TEXT    NOT NULL DEFAULT '', -- writing, ready, failed
	msg     TEXT    NOT NULL DEFAULT '', -- the message writing it
	error   TEXT    NOT NULL DEFAULT '',
	updated INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS wiki_pages (
	repo   TEXT    NOT NULL,
	slug   TEXT    NOT NULL,
	title  TEXT    NOT NULL,
	parent TEXT    NOT NULL DEFAULT '',
	pos    INTEGER NOT NULL,
	body   TEXT    NOT NULL,
	PRIMARY KEY (repo, slug)
) WITHOUT ROWID;
`

// Wiki is a repository's wiki.
type Wiki struct {
	Repo    string    `json:"repo"`
	Agent   string    `json:"agent"`
	Lang    string    `json:"lang,omitempty"`
	Status  string    `json:"status"`
	Msg     string    `json:"msg,omitempty"`
	Error   string    `json:"error,omitempty"`
	Updated time.Time `json:"updated"`
	Pages   int       `json:"pages"`
}

// WikiPage is a page of it.
type WikiPage struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Parent string `json:"parent,omitempty"`
	Body   string `json:"body,omitempty"`
}

// ErrNoWiki: no wiki for that repository.
var ErrNoWiki = errors.New("no wiki for this repository")

func (s *Store) Wikis(ctx context.Context) ([]Wiki, error) {
	rows, err := s.r.QueryContext(ctx, wikiSelect+` ORDER BY w.repo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Wiki{}
	for rows.Next() {
		w, err := scanWiki(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Wiki is one repository's wiki (one row, not the table).
func (s *Store) Wiki(ctx context.Context, repo string) (Wiki, error) {
	w, err := scanWiki(s.r.QueryRowContext(ctx, wikiSelect+` WHERE w.repo = ?`, repo))
	if errors.Is(err, sql.ErrNoRows) {
		return Wiki{}, ErrNoWiki
	}
	return w, err
}

const wikiSelect = `SELECT w.repo, w.agent, w.lang, w.status, w.msg, w.error, w.updated,
	(SELECT count(*) FROM wiki_pages p WHERE p.repo = w.repo) FROM wikis w`

func scanWiki(row interface{ Scan(...any) error }) (Wiki, error) {
	var w Wiki
	var upd int64
	if err := row.Scan(&w.Repo, &w.Agent, &w.Lang, &w.Status, &w.Msg, &w.Error, &upd, &w.Pages); err != nil {
		return Wiki{}, err
	}
	w.Updated = fromMS(upd)
	return w, nil
}

// StartWiki notes a wiki being written by msg (its pages, if any, kept until the new ones come).
// False when one is being written already.
func (s *Store) StartWiki(ctx context.Context, repo, agent, lang, msg string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO wikis (repo, agent, lang, status, msg, error, updated) VALUES (?, ?, ?, 'writing', ?, '', ?)
		ON CONFLICT (repo) DO UPDATE SET agent = excluded.agent, lang = excluded.lang, status = 'writing', msg = excluded.msg,
			error = '', updated = excluded.updated WHERE wikis.status <> 'writing'`,
		repo, agent, lang, msg, ms(time.Now()))
	if err != nil {
		return false, err
	}
	k, _ := res.RowsAffected()
	return k == 1, nil
}

// FinishWiki puts the pages msg wrote in place of the old ones (or notes why there are none), if
// msg is still the one writing it.
func (s *Store) FinishWiki(ctx context.Context, repo, msg string, pages []WikiPage, errText string) error {
	return s.tx(ctx, func(t *sql.Tx) error {
		status := "ready"
		if errText != "" {
			status = "failed"
		}
		res, err := t.ExecContext(ctx, `UPDATE wikis SET status = ?, error = ?, updated = ? WHERE repo = ? AND msg = ? AND status = 'writing'`,
			status, errText, ms(time.Now()), repo, msg)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 || errText != "" {
			return nil
		}
		if _, err := t.ExecContext(ctx, `DELETE FROM wiki_pages WHERE repo = ?`, repo); err != nil {
			return err
		}
		for i, p := range pages {
			if _, err := t.ExecContext(ctx, `INSERT INTO wiki_pages (repo, slug, title, parent, pos, body) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (repo, slug) DO NOTHING`, repo, p.Slug, p.Title, p.Parent, i, p.Body); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetWikiMsg puts the task's message in place of the reservation it was sent under.
func (s *Store) SetWikiMsg(ctx context.Context, repo, from, to string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE wikis SET msg = ? WHERE repo = ? AND msg = ? AND status = 'writing'`, to, repo, from)
	return err
}

// WikiPages lists a wiki's pages in order, with their text when full.
func (s *Store) WikiPages(ctx context.Context, repo string, full bool) ([]WikiPage, error) {
	col := "''"
	if full {
		col = "body"
	}
	rows, err := s.r.QueryContext(ctx, `SELECT slug, title, parent, `+col+` FROM wiki_pages WHERE repo = ? ORDER BY pos`, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WikiPage{}
	for rows.Next() {
		var p WikiPage
		if err := rows.Scan(&p.Slug, &p.Title, &p.Parent, &p.Body); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWiki(ctx context.Context, repo string) error {
	return s.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `DELETE FROM wikis WHERE repo = ?`, repo)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return ErrNoWiki
		}
		_, err = t.ExecContext(ctx, `DELETE FROM wiki_pages WHERE repo = ?`, repo)
		return err
	})
}

// WikiEntry is a page of any repository's wiki, for a view of them all.
type WikiEntry struct {
	Repo    string    `json:"repo"`
	Slug    string    `json:"slug"`
	Title   string    `json:"title"`
	Parent  string    `json:"parent,omitempty"`
	Body    string    `json:"body"`
	Updated time.Time `json:"updated"` // its wiki's
}

// WikiEntries lists the pages of every wiki with their text, by repository and slug, after the
// cursor (empty: from the start): up to limit pages and about maxBytes of text (at least one page
// whatever its size). next is the cursor of the rest, empty when there is none.
func (s *Store) WikiEntries(ctx context.Context, after string, limit, maxBytes int) (out []WikiEntry, next string, err error) {
	repo, slug, _ := strings.Cut(after, "\x00")
	rows, err := s.r.QueryContext(ctx, `SELECT p.repo, p.slug, p.title, p.parent, p.body, w.updated
		FROM wiki_pages p JOIN wikis w ON w.repo = p.repo
		WHERE p.repo > ? OR (p.repo = ? AND p.slug > ?) ORDER BY p.repo, p.slug LIMIT ?`, repo, repo, slug, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out = []WikiEntry{}
	size := 0
	for rows.Next() {
		if len(out) == limit || (len(out) > 0 && size >= maxBytes) {
			last := out[len(out)-1]
			return out, last.Repo + "\x00" + last.Slug, nil
		}
		var e WikiEntry
		var upd int64
		if err := rows.Scan(&e.Repo, &e.Slug, &e.Title, &e.Parent, &e.Body, &upd); err != nil {
			return nil, "", err
		}
		e.Updated = fromMS(upd)
		size += len(e.Body)
		out = append(out, e)
	}
	return out, "", rows.Err()
}

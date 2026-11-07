package store

import (
	"context"
	"database/sql"
	"errors"
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
	rows, err := s.r.QueryContext(ctx, `SELECT w.repo, w.agent, w.lang, w.status, w.msg, w.error, w.updated,
		(SELECT count(*) FROM wiki_pages p WHERE p.repo = w.repo) FROM wikis w ORDER BY w.repo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Wiki{}
	for rows.Next() {
		var w Wiki
		var upd int64
		if err := rows.Scan(&w.Repo, &w.Agent, &w.Lang, &w.Status, &w.Msg, &w.Error, &upd, &w.Pages); err != nil {
			return nil, err
		}
		w.Updated = fromMS(upd)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) Wiki(ctx context.Context, repo string) (Wiki, error) {
	all, err := s.Wikis(ctx)
	if err != nil {
		return Wiki{}, err
	}
	for _, w := range all {
		if w.Repo == repo {
			return w, nil
		}
	}
	return Wiki{}, ErrNoWiki
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

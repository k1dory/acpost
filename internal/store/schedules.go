package store

import (
	"context"
	"errors"
	"time"
)

// Schedules: tasks the node sends on a cron schedule (a nightly test run, a daily check), each run
// a new thread.

const schedulesSchema = `
CREATE TABLE IF NOT EXISTS schedules (
	id       TEXT    PRIMARY KEY,
	name     TEXT    NOT NULL,
	cron     TEXT    NOT NULL,
	agent    TEXT    NOT NULL,
	body     TEXT    NOT NULL,
	cwd      TEXT    NOT NULL DEFAULT '',
	model    TEXT    NOT NULL DEFAULT '',
	effort   TEXT    NOT NULL DEFAULT '',
	enabled  INTEGER NOT NULL DEFAULT 1,
	next_run INTEGER NOT NULL DEFAULT 0,
	last_run INTEGER NOT NULL DEFAULT 0,
	last_msg TEXT    NOT NULL DEFAULT '',
	last_err TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;
`

// Schedule is a task sent on a cron schedule.
type Schedule struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Cron    string    `json:"cron"` // 5 fields, or @daily, @hourly…
	Agent   string    `json:"agent"`
	Body    string    `json:"body"`
	Cwd     string    `json:"cwd,omitempty"`
	Model   string    `json:"model,omitempty"`
	Effort  string    `json:"effort,omitempty"`
	Enabled bool      `json:"enabled"`
	NextRun time.Time `json:"next_run,omitzero"`
	LastRun time.Time `json:"last_run,omitzero"`
	LastMsg string    `json:"last_msg,omitempty"` // the thread of the last run
	LastErr string    `json:"last_err,omitempty"`
}

// ErrNoSchedule: no such schedule.
var ErrNoSchedule = errors.New("no such schedule")

func (s *Store) Schedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, name, cron, agent, body, cwd, model, effort, enabled, next_run, last_run, last_msg, last_err
		FROM schedules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var sc Schedule
		var next, last int64
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.Cron, &sc.Agent, &sc.Body, &sc.Cwd, &sc.Model, &sc.Effort, &sc.Enabled, &next, &last, &sc.LastMsg, &sc.LastErr); err != nil {
			return nil, err
		}
		sc.NextRun, sc.LastRun = fromMS(next), fromMS(last)
		out = append(out, sc)
	}
	return out, rows.Err()
}

// PutSchedule adds or replaces a schedule (its last run kept).
func (s *Store) PutSchedule(ctx context.Context, sc Schedule) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO schedules (id, name, cron, agent, body, cwd, model, effort, enabled, next_run)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, cron = excluded.cron, agent = excluded.agent, body = excluded.body,
			cwd = excluded.cwd, model = excluded.model, effort = excluded.effort, enabled = excluded.enabled, next_run = excluded.next_run`,
		sc.ID, sc.Name, sc.Cron, sc.Agent, sc.Body, sc.Cwd, sc.Model, sc.Effort, sc.Enabled, ms(sc.NextRun))
	return err
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNoSchedule
	}
	return nil
}

// TakeDue claims a schedule due at now: its next run moved to next in the same step (two nodes on
// one store, or a slow tick, never run it twice). False when it was not due any more.
func (s *Store) TakeDue(ctx context.Context, id string, due, next time.Time) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE schedules SET next_run = ?, last_run = ? WHERE id = ? AND enabled = 1 AND next_run = ?`,
		ms(next), time.Now().UnixMilli(), id, ms(due))
	if err != nil {
		return false, err
	}
	k, _ := res.RowsAffected()
	return k == 1, nil
}

// RanSchedule notes a run's outcome: the message it sent, or why it could not.
func (s *Store) RanSchedule(ctx context.Context, id, msg, errText string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE schedules SET last_msg = ?, last_err = ? WHERE id = ?`, msg, errText, id)
	return err
}

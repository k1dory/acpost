package store

import (
	"context"
	"database/sql"
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
	last_err TEXT    NOT NULL DEFAULT '',
	pending  INTEGER NOT NULL DEFAULT 0 -- an occurrence claimed and not yet sent (ms of its due time)
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
	Pending time.Time `json:"-"` // an occurrence claimed and not yet sent
}

func migrateSchedules(s *Store) error {
	var n int
	if err := s.w.QueryRow(`SELECT count(*) FROM pragma_table_info('schedules') WHERE name = 'pending'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := s.w.Exec(`ALTER TABLE schedules ADD COLUMN pending INTEGER NOT NULL DEFAULT 0`)
		return err
	}
	return nil
}

// ErrNoSchedule: no such schedule.
var ErrNoSchedule = errors.New("no such schedule")

func (s *Store) Schedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+scheduleCols+` FROM schedules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

const scheduleCols = `id, name, cron, agent, body, cwd, model, effort, enabled, next_run, last_run, last_msg, last_err, pending`

func scanSchedule(row interface{ Scan(...any) error }) (Schedule, error) {
	var sc Schedule
	var next, last, pending int64
	if err := row.Scan(&sc.ID, &sc.Name, &sc.Cron, &sc.Agent, &sc.Body, &sc.Cwd, &sc.Model, &sc.Effort, &sc.Enabled, &next, &last, &sc.LastMsg, &sc.LastErr, &pending); err != nil {
		return Schedule{}, err
	}
	sc.NextRun, sc.LastRun, sc.Pending = fromMS(next), fromMS(last), fromMS(pending)
	return sc, nil
}

// PutSchedule adds or replaces a schedule (its last run kept; disabled, an occurrence pending is dropped).
func (s *Store) PutSchedule(ctx context.Context, sc Schedule) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO schedules (id, name, cron, agent, body, cwd, model, effort, enabled, next_run)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, cron = excluded.cron, agent = excluded.agent, body = excluded.body,
			cwd = excluded.cwd, model = excluded.model, effort = excluded.effort, enabled = excluded.enabled, next_run = excluded.next_run,
			pending = CASE WHEN excluded.enabled = 0 THEN 0 ELSE schedules.pending END`,
		sc.ID, sc.Name, sc.Cron, sc.Agent, sc.Body, sc.Cwd, sc.Model, sc.Effort, sc.Enabled, ms(sc.NextRun))
	return err
}

// Schedule is one schedule (the scheduler asks for each due one: one row, not the table).
func (s *Store) Schedule(ctx context.Context, id string) (Schedule, error) {
	sc, err := scanSchedule(s.r.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ErrNoSchedule
	}
	return sc, err
}

// FailedSchedule notes why a pending occurrence could not be sent yet; it stays pending.
func (s *Store) FailedSchedule(ctx context.Context, id string, occurrence time.Time, errText string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE schedules SET last_err = ? WHERE id = ? AND pending = ?`, errText, id, ms(occurrence))
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

// TakeDue claims an occurrence due at due: in one step its next run moves to next and the
// occurrence is noted pending (two ticks, or two nodes on one store, never claim it twice; a crash
// before it is sent leaves it to send again). False when it was not due any more.
func (s *Store) TakeDue(ctx context.Context, id string, due, next time.Time) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE schedules SET next_run = ?, pending = ? WHERE id = ? AND enabled = 1 AND next_run = ? AND pending = 0`,
		ms(next), ms(due), id, ms(due))
	if err != nil {
		return false, err
	}
	k, _ := res.RowsAffected()
	return k == 1, nil
}

// RanSchedule notes a run's outcome (the message it sent, or why it could not) and when; a pending
// occurrence it was is done.
func (s *Store) RanSchedule(ctx context.Context, id string, occurrence time.Time, msg, errText string) error {
	q := `UPDATE schedules SET last_run = ?, last_msg = ?, last_err = ? WHERE id = ?`
	args := []any{time.Now().UnixMilli(), msg, errText, id}
	if !occurrence.IsZero() {
		q = `UPDATE schedules SET last_run = ?, last_msg = ?, last_err = ?, pending = 0 WHERE id = ? AND pending = ?`
		args = append(args, ms(occurrence))
	}
	_, err := s.w.ExecContext(ctx, q, args...)
	return err
}

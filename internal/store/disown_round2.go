package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A joined node that sent a message through its hub and never had the answer asks the hub, after
// a while, to disown the send: the hub either has it (under its id, or under the sender's key) and
// says so, or records the id as disowned, refusing a send of it that arrives later. Either way the
// node then knows: the copy follows the hub's message, or nothing will ever run.

const disownSchema = `
CREATE TABLE IF NOT EXISTS disowned (
	id     TEXT PRIMARY KEY,
	sender TEXT NOT NULL,
	at     INTEGER NOT NULL
) WITHOUT ROWID;
`

// disownKeep is how long a disowned id is refused: far past any send held on its way.
const disownKeep = 7 * 24 * time.Hour

// ErrDisowned: the message's id was disowned by its sender (Disown); it is not taken.
var ErrDisowned = errors.New("the sender gave this message up")

// Disown answers a sender's question about a message it sent (id, and key, its idempotency key or
// ""): the message as stored, found true, when there is one under the id or the sender's key;
// otherwise the id is recorded as disowned, in the same transaction, so no later insert of it
// succeeds (InsertLimited), and found is false.
func (s *Store) Disown(ctx context.Context, sender, id, key string) (Message, bool, error) {
	var m Message
	found := false
	err := s.tx(ctx, func(t *sql.Tx) error {
		var hit string
		err := t.QueryRowContext(ctx, "SELECT id FROM messages WHERE id = ?", id).Scan(&hit)
		if errors.Is(err, sql.ErrNoRows) && key != "" {
			err = t.QueryRowContext(ctx, "SELECT id FROM messages WHERE sender = ? AND idem_key = ?", sender, key).Scan(&hit)
		}
		switch {
		case err == nil:
			found = true
			m, err = s.get(ctx, t, hit)
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		now := time.Now()
		if _, err := t.ExecContext(ctx, "DELETE FROM disowned WHERE at < ?", ms(now.Add(-disownKeep))); err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, "INSERT OR IGNORE INTO disowned (id, sender, at) VALUES (?, ?, ?)", id, sender, ms(now))
		return err
	})
	return m, found, err
}

// isDisowned says whether id was disowned (within t, the insert's transaction).
func isDisowned(ctx context.Context, t *sql.Tx, id string) (bool, error) {
	var n int
	err := t.QueryRowContext(ctx, "SELECT count(*) FROM disowned WHERE id = ?", id).Scan(&n)
	return n > 0, err
}

package store

import (
	"context"
	"database/sql"
	"errors"
)

// DeleteBacklogItemIf removes an item only as it was read (b): one changed since stays. It
// reports whether it removed it.
func (s *Store) DeleteBacklogItemIf(ctx context.Context, b BacklogItem) (bool, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM backlog WHERE id = ? AND title = ? AND body = ? AND agent = ? AND cwd = ?`,
		b.ID, b.Title, b.Body, b.Agent, b.Cwd)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ByIdemPrefix returns the latest message a sender sent with an idempotency key that starts with
// prefix (ErrNotFound: none).
func (s *Store) ByIdemPrefix(ctx context.Context, sender, prefix string) (Message, error) {
	var id string
	// A range on the key keeps to the index (sender, idem_key); \xff is above any key's bytes.
	err := s.r.QueryRowContext(ctx, `SELECT id FROM messages WHERE sender = ? AND idem_key >= ? AND idem_key < ? ORDER BY seq DESC LIMIT 1`,
		sender, prefix, prefix+"\xff").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return s.Get(ctx, id)
}

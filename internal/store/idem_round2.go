package store

import (
	"context"
	"database/sql"
	"errors"
)

// ByIdemKey returns the message sent with idempotency key key, by any sender: for a key made
// unique by its maker (a wiki's task), looked up when the link to the message was not kept.
// (Rare: it is not on an index of its own.)
func (s *Store) ByIdemKey(ctx context.Context, key string) (Message, error) {
	var id string
	err := s.r.QueryRowContext(ctx, "SELECT id FROM messages WHERE idem_key = ? ORDER BY seq LIMIT 1", key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return s.Get(ctx, id)
}

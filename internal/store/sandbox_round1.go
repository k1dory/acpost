package store

import (
	"context"
	"strconv"
	"time"
)

// A sandbox agent's cursor: how far it has read its sandbox's conversation, so a restarted node
// reads on from there instead of from the start. It is kept in the sessions table under a key no
// session uses ("sandbox-cursor:…"), with no schema of its own; GC lets it go with the sessions
// unused past retention (the next turn reads the history again, as a first one does).

const sandboxCursorKey = "sandbox-cursor:"

// SandboxCursor is where an agent last read the conversation that where names (0: not known).
func (s *Store) SandboxCursor(ctx context.Context, agent, where string) (int64, error) {
	v, err := s.Session(ctx, agent, sandboxCursorKey+where)
	if err != nil || v == "" {
		return 0, err
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n, nil
}

// PutSandboxCursor records the cursor, only ever forward.
func (s *Store) PutSandboxCursor(ctx context.Context, agent, where string, cursor int64) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO sessions (agent, key, session_id, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (agent, key) DO UPDATE SET session_id = excluded.session_id, updated_at = excluded.updated_at
		WHERE CAST(sessions.session_id AS INTEGER) <= CAST(excluded.session_id AS INTEGER)`,
		agent, sandboxCursorKey+where, strconv.FormatInt(cursor, 10), ms(time.Now()))
	return err
}

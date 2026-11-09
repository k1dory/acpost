package node

import (
	"context"
	"errors"
	"strings"

	"github.com/k1dory/acpost/internal/store"
)

// Disown is a joined node giving up a message it sent through this hub as from (key: its
// idempotency key, or "") and never had the answer for: found, the hub has it and the node's copy
// follows it; not found, the id is refused from now on (a send of it that comes late is not run).
func (n *Node) Disown(ctx context.Context, p Principal, id, from, key string) (store.Message, bool, error) {
	if p.Kind != "node" {
		return store.Message{}, false, errf("refused", "only a joined node disowns its sends")
	}
	if fa, err := ParseAddress(from); err != nil || fa.Node != p.Name {
		return store.Message{}, false, errf("refused", "node %s may only disown its own sends (…@%s)", p.Name, p.Name)
	}
	if !strings.HasPrefix(id, "msg_") || len(id) != 30 {
		return store.Message{}, false, errf("invalid", "bad message id %q", id)
	}
	if len(key) > maxLabel {
		return store.Message{}, false, errf("invalid", "the idempotency key is %d bytes, over %d", len(key), maxLabel)
	}
	m, found, err := n.st.Disown(ctx, from, id, key)
	if err != nil {
		return store.Message{}, false, errf("unavailable", "%v", err)
	}
	if found && m.From != from {
		return store.Message{}, false, errf("refused", "message %s is not %s's", id, from) // another sender's id
	}
	return hideLease(m), found, nil
}

// disownedErr is what a send of a disowned message gets.
func disownedErr(err error, id string) error {
	if errors.Is(err, store.ErrDisowned) {
		return errf("refused", "message %s was given up by its sender: not taken", id)
	}
	return nil
}

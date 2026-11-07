package node

import (
	"context"
	"errors"
	"strings"

	"github.com/k1dory/acpost/internal/store"
)

// The backlog: tasks for later, the node user's; started, an item becomes a message to its agent
// (in its directory) and leaves the backlog.

func (n *Node) Backlog(ctx context.Context, p Principal) ([]store.BacklogItem, error) {
	if err := userOnly(p, "the backlog"); err != nil {
		return nil, err
	}
	out, err := n.st.Backlog(ctx)
	if err != nil {
		return nil, errf("unavailable", "%v", err)
	}
	return out, nil
}

// PutBacklogItem adds an item (no id) or changes one.
func (n *Node) PutBacklogItem(ctx context.Context, p Principal, b store.BacklogItem) (store.BacklogItem, error) {
	if err := userOnly(p, "the backlog"); err != nil {
		return b, err
	}
	b.Title = strings.TrimSpace(b.Title)
	switch {
	case b.Title == "" || len(b.Title) > 300:
		return b, errf("invalid", "a title: 1 to 300 characters")
	case len(b.Body) > int(n.cfg.Limits.MaxBody):
		return b, errf("invalid", "the text is over limits.max_body")
	}
	if b.Agent != "" {
		to, err := ParseAddress(b.Agent)
		if err != nil || to.Kind != "agent" {
			return b, errf("invalid", "agent %q: agent:name", b.Agent)
		}
		b.Agent = to.String()
	}
	if b.Cwd != "" {
		dir, err := checkDir(b.Cwd)
		if err != nil {
			return b, err
		}
		b.Cwd = dir
	}
	if b.ID == "" {
		b.ID = "bl_" + store.NewID()[4:]
	}
	if err := n.st.PutBacklogItem(ctx, b); err != nil {
		return b, errf("unavailable", "%v", err)
	}
	return b, nil
}

func (n *Node) DeleteBacklogItem(ctx context.Context, p Principal, id string) error {
	if err := userOnly(p, "the backlog"); err != nil {
		return err
	}
	if _, err := n.st.TakeBacklogItem(ctx, id); errors.Is(err, store.ErrNoBacklogItem) {
		return errf("not_found", "%v", err)
	} else if err != nil {
		return errf("unavailable", "%v", err)
	}
	return nil
}

// StartBacklogItem sends an item to its agent (or agent, when it names none) in its directory: the
// message. The item leaves the backlog; if the message cannot be sent, it comes back.
func (n *Node) StartBacklogItem(ctx context.Context, p Principal, id, agent string) (store.Message, error) {
	if err := userOnly(p, "the backlog"); err != nil {
		return store.Message{}, err
	}
	b, err := n.st.TakeBacklogItem(ctx, id)
	if err != nil {
		return store.Message{}, errf("not_found", "%v", err)
	}
	to := cmpOr(agent, b.Agent)
	if to == "" {
		_ = n.st.PutBacklogItem(context.WithoutCancel(ctx), b)
		return store.Message{}, errf("invalid", "the agent to give it to")
	}
	body := b.Title
	if strings.TrimSpace(b.Body) != "" {
		body += "\n\n" + b.Body
	}
	m, _, err := n.Send(ctx, p, SendRequest{To: to, Body: body, Cwd: b.Cwd})
	if err != nil {
		_ = n.st.PutBacklogItem(context.WithoutCancel(ctx), b) // back where it was
		return m, err
	}
	return m, nil
}

package node

// Messages into existing ACP sessions (send --session): who may, and which session they mean.

import (
	"context"
	"strings"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// maxSessionPages bounds how far a lookup reads an agent's session list.
const maxSessionPages = 20

// resolveSession checks a message's session: who may continue it, which one "latest" is, and the
// session's working directory. The session and its directory go into meta keys only acpost sets.
func (n *Node) resolveSession(ctx context.Context, p Principal, to Address, r *SendRequest) error {
	if p.Kind != "node" { // a node forwards keys its own node set after these checks
		for k := range r.Meta {
			if strings.HasPrefix(k, "acp_") {
				return errh("invalid", "use --session to continue a session", "meta key %q: keys starting with acp_ are set by acpost", k)
			}
		}
	} else {
		// The session a node forwards is the one its sender named; where it runs is this node's to
		// say (from the agent's own list), never the forwarding node's.
		delete(r.Meta, metaSessionCwd)
		if sid := r.Meta[metaSession]; sid != "" {
			if cfg := n.agentCfg(to.Name); cfg != nil && cfg.Driver == "acp" {
				if cwd := n.sessionCwd(ctx, to.Name, sid); cwd != "" {
					r.Meta[metaSessionCwd] = cwd
				}
			}
		}
	}
	if r.Session == "" {
		return nil
	}
	if len(r.Session) > 256 || strings.ContainsFunc(r.Session, func(c rune) bool { return c < ' ' || c == 0x7f }) {
		return errf("invalid", "session: up to 256 printable characters")
	}
	if p.Kind == "node" {
		return nil // checked on the sending node
	}
	cfg := n.agentCfg(to.Name)
	local := cfg != nil && cfg.Driver == "acp" && (to.Node == "" || to.Node == n.Name())
	if !local {
		if r.Session == "latest" {
			return errh("invalid", "name the session id (`acpost sessions --agent <name>` on its node)", "--session latest needs an acp agent of this node, not %s", to)
		}
		r.Meta = withMeta(r.Meta, metaSession, r.Session, "")
		return nil
	}
	if err := canUseSessions(p, to.Name, cfg); err != nil {
		return err
	}
	var found client.SessionInfo
	cursor := ""
	for page := 0; page < maxSessionPages; page++ {
		list, next, err := n.Sessions(ctx, p, to.Name, "", cursor)
		if CodeOf(err) == "unsupported" && r.Session != "latest" {
			break // the agent cannot list: try the id as given, in the agent's own directory
		}
		if err != nil {
			return err
		}
		for _, si := range list {
			if r.Session == "latest" && (found.ID == "" || newer(si.UpdatedAt, found.UpdatedAt)) || si.ID == r.Session {
				found = si
			}
		}
		if next == "" || (r.Session != "latest" && found.ID != "") {
			break
		}
		cursor = next
	}
	switch {
	case found.ID != "":
		r.Session = found.ID
	case r.Session == "latest":
		return errh("not_found", "send without --session to start a conversation", "agent:%s has no sessions", to.Name)
	}
	r.Meta = withMeta(r.Meta, metaSession, r.Session, found.Cwd)
	return nil
}

func withMeta(meta map[string]string, key, session, cwd string) map[string]string {
	out := make(map[string]string, len(meta)+2)
	for k, v := range meta {
		out[k] = v
	}
	out[key] = session
	if cwd != "" {
		out[metaSessionCwd] = cwd
	}
	return out
}

// canUseSessions: an agent's sessions are its conversations with whoever started them, so only the
// node's own user and agents may list or continue them, and only where allow_from lets them in.
func canUseSessions(p Principal, agent string, cfg *config.Agent) error {
	if !p.local() {
		return errh("refused", "sessions are the agent's conversations: only the node's own user and agents may list or continue them",
			"%s may not use the sessions of agent:%s", p.Address(), agent)
	}
	if !matchAny(cfg.AllowFrom, p.Address()) {
		return errf("refused", "agent:%s does not take messages from %s (allow_from)", agent, p.Address())
	}
	return nil
}

// checkSelfSend refuses a message an agent sends to itself that could only wait for the turn it is
// sent from: one slot, or the same session as that turn.
func (n *Node) checkSelfSend(ctx context.Context, m *store.Message, agent string, cfg *config.Agent) error {
	if cfg.Concurrency < 2 {
		return errh("refused", "send to another agent, or give this one concurrency: 2 or more in the node's config",
			"agent:%s cannot message itself: its only slot is running the turn you are sending from, so the message would wait forever", agent)
	}
	parent, err := n.st.Get(ctx, m.Parent)
	if err != nil {
		return nil
	}
	child := *m
	if child.Thread == "" {
		child.Thread = child.ID
	}
	if k := sessionKey(cfg.Session, child); k != "" && k == sessionKey(cfg.Session, parent) {
		return errh("refused", "give the message another --task, so it gets a session of its own",
			"this message would need the session of the turn you are sending from (%s), and wait for it forever", k)
	}
	return nil
}

// sessionCwd is an ACP session's directory as its agent lists it ("" when not found).
func (n *Node) sessionCwd(ctx context.Context, agent, sid string) string {
	cursor := ""
	for page := 0; page < maxSessionPages; page++ {
		list, next, err := n.Sessions(ctx, Principal{Kind: "user", Name: n.login}, agent, "", cursor)
		if err != nil {
			return ""
		}
		for _, si := range list {
			if si.ID == sid {
				return si.Cwd
			}
		}
		if next == "" {
			return ""
		}
		cursor = next
	}
	return ""
}

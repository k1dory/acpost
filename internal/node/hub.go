package node

// Hub and joined nodes.
//
// A joined node announces its agents to the hub and long-polls the hub for messages to them; each
// becomes a local message (origin "hub:<lease>"), and when it is final its outcome goes back to
// the hub under the hub's lease. A message to an agent the node does not have goes to the hub; the
// node keeps a local copy (origin "remote") that follows the hub's until it is final, so `wait`,
// `show` and `list` work the same for local and remote messages.

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

const (
	originRemote = "remote" // a local copy of a message sent through the hub
	originHub    = "hub:"   // a message the hub handed to this node; the hub's lease follows

	announceEvery = 20 * time.Second
	announceTTL   = 70 * time.Second
	hubLease      = 2 * time.Minute
)

type registry struct {
	mu    sync.Mutex
	nodes map[string]nodeEntry
}

type nodeEntry struct {
	agents []string
	seen   time.Time
}

type remoteAgent struct{ agent, node string }

func newRegistry() *registry { return &registry{nodes: map[string]nodeEntry{}} }

func (r *registry) announce(node string, agents []string) {
	r.mu.Lock()
	r.nodes[node] = nodeEntry{agents: slices.Clone(agents), seen: time.Now()}
	r.mu.Unlock()
}

func (r *registry) live() map[string][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string][]string{}
	for node, e := range r.nodes {
		if time.Since(e.seen) < announceTTL {
			out[node] = e.agents
		}
	}
	return out
}

func (r *registry) nodesFor(to Address) []string {
	var out []string
	for node, agents := range r.live() {
		if (to.Node == "" || to.Node == node) && slices.Contains(agents, to.Name) {
			out = append(out, node)
		}
	}
	sort.Strings(out)
	return out
}

func (r *registry) has(to Address) bool { return len(r.nodesFor(to)) > 0 }

func (r *registry) resolve(to Address) (string, error) {
	nodes := r.nodesFor(to)
	switch len(nodes) {
	case 0:
		return "", errf("not_found", "no node serves %s", to)
	case 1:
		return nodes[0], nil
	}
	return "", errf("invalid", "%s is served by several nodes (%s): name one, agent:%s@<node>", to, strings.Join(nodes, ", "), to.Name)
}

func (r *registry) agentsOf(node string) []string { return r.live()[node] }

func (r *registry) list() []remoteAgent {
	var out []remoteAgent
	for node, agents := range r.live() {
		for _, a := range agents {
			out = append(out, remoteAgent{agent: a, node: node})
		}
	}
	return out
}

// Announce records the agents a joined node serves.
func (n *Node) Announce(ctx context.Context, p Principal, node string, agents []string) error {
	if p.Kind != "node" || p.Name != node {
		return errf("refused", "only node %s may announce for itself", node)
	}
	for _, a := range agents {
		if _, err := ParseAddress("agent:" + a); err != nil {
			return errf("invalid", "%v", err)
		}
	}
	n.remote.announce(node, agents)
	return nil
}

// NodeClaim hands a joined node the next message for one of the agents it announced.
func (n *Node) NodeClaim(ctx context.Context, p Principal, node string, wait, lease time.Duration) (store.Message, bool, error) {
	if p.Kind != "node" || p.Name != node {
		return store.Message{}, false, errf("refused", "only node %s may take its messages", node)
	}
	var recipients []string
	for _, a := range n.remote.agentsOf(node) {
		recipients = append(recipients, "agent:"+a+"@"+node)
	}
	if len(recipients) == 0 {
		select {
		case <-time.After(min(wait, time.Second)):
		case <-ctx.Done():
		}
		return store.Message{}, false, nil
	}
	return n.claim(ctx, recipients, wait, lease)
}

// qualify makes a local sender address global: user:lev → user:lev@laptop.
func (n *Node) qualify(addr string) string {
	if strings.Contains(addr, "@") {
		return addr
	}
	return addr + "@" + n.Name()
}

func hubErr(err error) error {
	var ce *client.Error
	if errors.As(err, &ce) {
		return &Error{Code: ce.Code, Message: "hub: " + ce.Message}
	}
	if errors.Is(err, client.ErrUnavailable) {
		return errf("unavailable", "%v", err)
	}
	return err
}

func isFinalErr(err error) bool {
	var ce *client.Error
	return errors.As(err, &ce) && ce.Code == "conflict"
}

// forward sends a message to the hub and keeps a local copy that follows it.
func (n *Node) forward(ctx context.Context, m *store.Message, to Address) (store.Message, bool, error) {
	req := client.SendRequest{To: to.String(), Body: m.Body, Task: m.Task, Thread: m.Thread, Meta: m.Meta,
		ID: m.ID, From: n.qualify(m.From), Parent: m.Parent, Depth: m.Depth}
	if !m.ExpiresAt.IsZero() {
		req.TTL = time.Until(m.ExpiresAt).Round(time.Millisecond).String()
	}
	idem := ""
	if m.IdemKey != "" {
		idem = m.IdemKey
	}
	sent, created, err := n.hub.Send(ctx, req, idem)
	if err != nil {
		return store.Message{}, false, hubErr(err)
	}
	local := &store.Message{ID: sent.ID, From: m.From, To: sent.To, Task: sent.Task, Thread: sent.Thread, Parent: sent.Parent,
		Depth: sent.Depth, Meta: sent.Meta, Body: sent.Body, ExpiresAt: sent.ExpiresAt, Origin: originRemote}
	got, _, err := n.st.Insert(ctx, local)
	if err != nil {
		// Sent before (a repeat with the same key): the local copy exists.
		if existing, gerr := n.st.Get(ctx, sent.ID); gerr == nil {
			return hideLease(existing), false, nil
		}
		return store.Message{}, false, err
	}
	n.notify()
	n.goLoop(func() { n.follow(sent.ID) })
	return hideLease(got), created, nil
}

// follow copies the hub's outcome of a forwarded message onto its local copy.
func (n *Node) follow(id string) {
	for n.ctx.Err() == nil {
		m, err := n.hub.Wait(n.ctx, id, client.MaxWait)
		if err != nil {
			var ce *client.Error
			if errors.As(err, &ce) && ce.Code == "not_found" {
				_ = n.st.Settle(n.ctx, id, store.Failed, "", "the hub no longer has this message", 0)
				n.notify()
				return
			}
			select {
			case <-n.ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if m.State == store.Running || m.State.Final() {
			if err := n.st.Settle(n.ctx, id, m.State, m.Reply, m.Error, m.Attempts); err == nil {
				n.notify()
			}
		}
		if m.State.Final() {
			return
		}
	}
}

// announced are the agents this node offers through its hub: those that take messages from other nodes.
func (n *Node) announced() []string {
	var out []string
	for name, a := range n.cfg.Agents {
		for _, p := range a.AllowFrom {
			if p == "*" || strings.Contains(p, "@") {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func (n *Node) startJoin() {
	n.goLoop(func() {
		for {
			if err := n.hub.Announce(n.ctx, n.Name(), n.announced()); err != nil && n.ctx.Err() == nil {
				n.log.Warn("announce to hub", "err", err)
			}
			select {
			case <-n.ctx.Done():
				return
			case <-time.After(announceEvery):
			}
		}
	})
	n.goLoop(n.hubClaimLoop)
	// Pick up where the last process left off.
	if list, err := n.st.Unsettled(n.ctx, originRemote); err == nil {
		for _, it := range list {
			id := it[0]
			n.goLoop(func() { n.follow(id) })
		}
	}
	if list, err := n.st.Unsettled(n.ctx, originHub); err == nil {
		for _, it := range list {
			id, lease := it[0], strings.TrimPrefix(it[1], originHub)
			n.goLoop(func() { n.relay(id, lease) })
		}
	}
}

func (n *Node) hubClaimLoop() {
	for n.ctx.Err() == nil {
		m, ok, err := n.hub.NodeClaim(n.ctx, n.Name(), 30*time.Second, hubLease)
		if err != nil {
			if n.ctx.Err() != nil {
				return
			}
			n.log.Warn("take messages from hub", "err", err)
			select {
			case <-n.ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		if ok {
			n.deliver(m)
		}
	}
}

// deliver makes a message the hub handed over a local one for the agent it names.
func (n *Node) deliver(m store.Message) {
	ctx := n.ctx
	to, err := ParseAddress(m.To)
	refuse := func(why string) {
		_ = n.hub.Reply(ctx, m.ID, client.ReplyRequest{Lease: m.LeaseID, Error: why})
	}
	if err != nil || to.Node != n.Name() || n.agentCfg(to.Name) == nil {
		refuse("node " + n.Name() + " has no agent " + m.To)
		return
	}
	if !matchAny(n.agentCfg(to.Name).AllowFrom, m.From) {
		refuse("agent:" + to.Name + " does not take messages from " + m.From + " (allow_from)")
		return
	}
	local := &store.Message{ID: m.ID, From: m.From, To: "agent:" + to.Name, Task: m.Task, Thread: m.Thread, Parent: m.Parent,
		Depth: m.Depth, Meta: m.Meta, Body: m.Body, ExpiresAt: m.ExpiresAt, Origin: originHub + m.LeaseID}
	if _, _, err := n.st.Insert(ctx, local); err != nil {
		if _, gerr := n.st.Get(ctx, m.ID); gerr != nil {
			refuse("node " + n.Name() + ": " + err.Error())
			return
		}
		// Delivered again (the hub's lease ran out before): keep the copy, follow it under the new lease.
		_ = n.st.SetOrigin(ctx, m.ID, originHub+m.LeaseID)
	}
	n.notify()
	n.goLoop(func() { n.relay(m.ID, m.LeaseID) })
}

// relay keeps the hub's lease on a delivered message alive while it runs here, and hands the
// outcome back when it is final. A message the hub cancelled is cancelled here.
func (n *Node) relay(id, lease string) {
	for n.ctx.Err() == nil {
		m, err := n.Wait(n.ctx, n.LocalUser(), id, hubLease/3)
		if err != nil {
			return
		}
		if m.State.Final() {
			r := client.ReplyRequest{Lease: lease, Body: m.Reply}
			if m.State != store.Done {
				r.Error = m.Error
				if r.Error == "" {
					r.Error = string(m.State)
				}
			}
			if m.Origin != originHub+lease {
				return
			}
			for try := 0; n.ctx.Err() == nil; try++ {
				err := n.hub.Reply(n.ctx, id, r)
				if err == nil || isFinalErr(err) || try >= 20 {
					return
				}
				time.Sleep(time.Second)
			}
			return
		}
		if m.Origin != originHub+lease {
			return // delivered again under another lease: that delivery's relay takes over
		}
		if err := n.hub.Extend(n.ctx, id, lease, hubLease); err != nil && isFinalErr(err) {
			// The hub gave the message up (cancelled, or the lease went to another delivery).
			if cur, _ := n.st.Get(n.ctx, id); cur.Origin != originHub+lease {
				return
			}
			if _, err := n.st.Cancel(n.ctx, id); err == nil {
				n.notify()
			}
			return
		}
	}
}

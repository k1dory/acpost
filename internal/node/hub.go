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
	"fmt"
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
	m, ok, err := n.claim(ctx, recipients, wait, lease)
	if ok {
		// A sender of this node crosses to another one: its address names this node.
		m.From = n.qualify(m.From)
	}
	return m, ok, err
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

// fetchFiles downloads one side of a message's files from the hub, after checking what the listing
// announces against this node's limits, and reading no more than those limits allow. A *Error is
// final (over the limits, or not what was listed); any other error may pass.
func (n *Node) fetchFiles(ctx context.Context, id string, reply bool, listed []store.File, side string) ([]store.File, error) {
	if err := n.checkListed(listed, side); err != nil {
		return nil, err
	}
	// Base64 takes a third more, JSON a little per file.
	limit := int64(n.cfg.Limits.MaxFilesSize)/3*4 + int64(len(listed))*1024 + 1<<20
	files, err := n.hub.SideFiles(ctx, id, reply, limit)
	if errors.Is(err, client.ErrBadResponse) {
		return nil, errf("refused", "the hub sent %s's files over this node's limits or malformed: %v", side, err)
	}
	if err != nil {
		return nil, err // passing: the caller retries, or says the hub failed
	}
	if err := n.checkFiles(files, nil, side); err != nil {
		return nil, err
	}
	for i := range files {
		files[i].Reply = reply
	}
	return files, nil
}

// filesRefused says the hub refused a request for its files (limits, names), not for a passing cause.
func filesRefused(err error) bool {
	var ce *client.Error
	return errors.As(err, &ce) && (ce.Code == "refused" || ce.Code == "invalid" || (ce.Code == "conflict" && strings.Contains(ce.Message, "file")))
}

func isFinalErr(err error) bool {
	var ce *client.Error
	return errors.As(err, &ce) && ce.Code == "conflict"
}

// forward sends a message to the hub and keeps a local copy that follows it.
func (n *Node) forward(ctx context.Context, m *store.Message, to Address) (store.Message, bool, error) {
	req := client.SendRequest{To: to.String(), Body: m.Body, Task: m.Task, Thread: m.Thread, Meta: m.Meta, Files: m.Files,
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
	files := m.Files
	if !created {
		// A repeat: the hub kept the first send, whose files may differ from this one's.
		files = nil
		if listed := pick(sent.Files, false); len(listed) > 0 {
			got, err := n.fetchFiles(ctx, sent.ID, false, listed, "the message")
			var e *Error
			if err != nil && !errors.As(err, &e) {
				err = hubErr(err)
			}
			if err != nil {
				return store.Message{}, false, err
			}
			files = got
		}
	}
	local := &store.Message{ID: sent.ID, From: m.From, To: sent.To, Task: sent.Task, Thread: sent.Thread, Parent: sent.Parent,
		Depth: sent.Depth, Meta: sent.Meta, Body: sent.Body, ExpiresAt: sent.ExpiresAt, Origin: originRemote, Files: files}
	got, _, err := n.st.Insert(ctx, local)
	if err != nil {
		// Sent before (a repeat with the same key): the local copy exists.
		if existing, gerr := n.st.Get(ctx, sent.ID); gerr == nil {
			return hideLease(existing), false, nil
		}
		return store.Message{}, false, err
	}
	n.notify()
	n.startFollow(sent.ID)
	return hideLease(got), created, nil
}

// maxFollowers bounds the long polls a node keeps on its hub for messages it forwarded; the rest
// are checked by followSweep.
const maxFollowers = 64

func (n *Node) startFollow(id string) {
	n.followMu.Lock()
	if n.following[id] || len(n.following) >= maxFollowers {
		n.followMu.Unlock()
		return
	}
	n.following[id] = true
	n.followMu.Unlock()
	n.goLoop(func() {
		defer func() {
			n.followMu.Lock()
			delete(n.following, id)
			n.followMu.Unlock()
		}()
		n.follow(id)
	})
}

func (n *Node) wakeFollow() {
	select {
	case n.followWake <- struct{}{}:
	default:
	}
}

// followSweep checks, page by page, every forwarded message that is not final and has no long poll
// of its own, so a restart or a burst of forwards needs neither memory nor goroutines per message.
func (n *Node) followSweep() {
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-time.After(2 * time.Second):
		case <-n.followWake:
		}
		after := ""
		for n.ctx.Err() == nil {
			page, err := n.st.UnsettledPage(n.ctx, originRemote, after, store.MaxPage)
			if err != nil || len(page) == 0 {
				break
			}
			for _, it := range page {
				id := it[0]
				n.followMu.Lock()
				busy := n.following[id]
				n.followMu.Unlock()
				if busy {
					continue
				}
				ctx, done := context.WithTimeout(n.ctx, 10*time.Second)
				m, err := n.hub.Get(ctx, id)
				done()
				n.settleFrom(id, m, err)
			}
			after = page[len(page)-1][0]
		}
	}
}

// settleFrom copies what the hub said of a forwarded message onto the local copy; it reports
// whether the message is settled for good.
func (n *Node) settleFrom(id string, m store.Message, err error) bool {
	if err != nil {
		var ce *client.Error
		if errors.As(err, &ce) && ce.Code == "not_found" {
			_ = n.st.Settle(n.ctx, id, store.Failed, "", "agent_error", "the hub no longer has this message", 0)
			n.notify()
			return true
		}
		return false
	}
	if m.State == store.Running || m.State.Final() {
		var files []store.File
		state, code, why := m.State, m.ErrorCode, m.Error
		refuse := func(err error) {
			files, state, code = nil, store.Failed, "files_refused"
			why = fmt.Sprintf("the reply's files were refused by node %s: %v", n.Name(), err)
		}
		if m.State.Final() && len(replyFiles(m.Files)) > 0 {
			// The listing says what is coming: this node's limits hold before anything is read.
			if err := n.checkListed(replyFiles(m.Files), "the reply"); err != nil {
				refuse(err)
			} else {
				ctx, done := context.WithTimeout(n.ctx, time.Minute)
				got, err := n.fetchFiles(ctx, id, true, replyFiles(m.Files), "the reply")
				done()
				var e *Error
				switch {
				case errors.As(err, &e):
					refuse(err) // not what the listing said, or over the limits: it will not get better
				case err != nil:
					return false // the outcome is copied with its files, or not yet
				}
				files = got
			}
		}
		err := n.st.Settle(n.ctx, id, state, m.Reply, code, why, m.Attempts, files...)
		if errors.Is(err, store.ErrStoreFull) {
			refuse(err)
			err = n.st.Settle(n.ctx, id, state, m.Reply, code, why, m.Attempts)
		}
		if err == nil {
			n.notify()
		}
	}
	return m.State.Final()
}

// follow copies the hub's outcome of a forwarded message onto its local copy.
func (n *Node) follow(id string) {
	for n.ctx.Err() == nil {
		m, err := n.hub.Wait(n.ctx, id, client.MaxWait)
		if n.settleFrom(id, m, err) {
			return
		}
		if err != nil {
			select {
			case <-n.ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
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
			next := announceEvery
			if err := n.hub.Announce(n.ctx, n.Name(), n.announced()); err != nil && n.ctx.Err() == nil {
				n.log.Warn("announce to hub", "err", err)
				next = 2 * time.Second // the hub may be starting: try again soon
			}
			select {
			case <-n.ctx.Done():
				return
			case <-time.After(next):
			}
		}
	})
	n.goLoop(n.hubClaimLoop)
	// Forwarded messages: the sweep picks up those the last process followed.
	n.goLoop(n.followSweep)
	// Delivered messages: few (local queue limits bound them), each relayed under its hub lease.
	for after := ""; ; {
		page, err := n.st.UnsettledPage(n.ctx, originHub, after, store.MaxPage)
		if err != nil || len(page) == 0 {
			break
		}
		for _, it := range page {
			id, lease := it[0], strings.TrimPrefix(it[1], originHub)
			n.goLoop(func() { n.relay(id, lease) })
		}
		after = page[len(page)-1][0]
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
	// This node's limits hold for what comes through the hub too.
	lim := n.cfg.Limits
	switch {
	case len(m.Body) > int(lim.MaxBody):
		refuse(fmt.Sprintf("node %s: the body is over limits.max_body (%d)", n.Name(), lim.MaxBody))
		return
	case metaSize(m.Meta) > int(lim.MaxMeta):
		refuse(fmt.Sprintf("node %s: meta is over limits.max_meta (%d)", n.Name(), lim.MaxMeta))
		return
	case m.Depth > lim.MaxDepth:
		refuse(fmt.Sprintf("node %s: the chain is over limits.max_depth (%d)", n.Name(), lim.MaxDepth))
		return
	}
	var files []store.File
	var leased time.Time // when the hub last renewed its lease to this node
	if listed := pick(m.Files, false); len(listed) > 0 {
		var err error
		for try := 0; ; try++ {
			// The hub's lease is kept while the files come: a delivery it gave up is not run here.
			ectx, edone := context.WithTimeout(ctx, 10*time.Second)
			eerr := n.hub.Extend(ectx, m.ID, m.LeaseID, hubLease)
			edone()
			if eerr != nil && isFinalErr(eerr) {
				return
			}
			if eerr == nil {
				leased = time.Now()
			}
			fctx, done := context.WithTimeout(ctx, hubLease/3)
			files, err = n.fetchFiles(fctx, m.ID, false, listed, "the message")
			done()
			var e *Error
			if err == nil || errors.As(err, &e) || try == 2 || ctx.Err() != nil {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			refuse(fmt.Sprintf("node %s: %v", n.Name(), err))
			return
		}
		if leased.IsZero() || time.Since(leased) > hubLease*2/3 {
			// The hub has not confirmed the lease lately (it may have run out there, however late
			// the claim arrived): it delivers again, and that delivery runs.
			n.log.Warn("delivery outlived its hub lease; left to the next delivery", "id", m.ID)
			return
		}
	}
	local := &store.Message{ID: m.ID, From: m.From, To: "agent:" + to.Name, Task: m.Task, Thread: m.Thread, Parent: m.Parent,
		Depth: m.Depth, Meta: m.Meta, Body: m.Body, ExpiresAt: m.ExpiresAt, Origin: originHub + m.LeaseID, Files: files}
	if _, _, err := n.st.InsertLimited(ctx, local, lim.MaxQueuedPerAgent); err != nil {
		if errors.Is(err, store.ErrQueueFull) {
			refuse(fmt.Sprintf("node %s: %s has a full queue (limits.max_queued_per_agent)", n.Name(), local.To))
			return
		}
		prev, gerr := n.st.Get(ctx, m.ID)
		// Only an earlier delivery of this same message is adopted, never a local message with the id.
		if gerr != nil || !strings.HasPrefix(prev.Origin, originHub) || prev.To != local.To || prev.From != local.From {
			refuse("node " + n.Name() + ": message id " + m.ID + " is already in use here")
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
			if len(replyFiles(m.Files)) > 0 {
				files, err := n.st.FilesOf(n.ctx, id)
				if err != nil {
					return
				}
				r.Files = replyFiles(files)
			}
			if m.State != store.Done {
				r.Error, r.ErrorCode = m.Error, m.ErrorCode
				if r.Error == "" {
					r.Error = string(m.State)
				}
			}
			if m.Origin != originHub+lease {
				return
			}
			for try := 0; n.ctx.Err() == nil; try++ {
				err := n.hub.Reply(n.ctx, id, r)
				if err != nil && len(r.Files) > 0 && filesRefused(err) {
					// The hub takes no such files: the sender still gets the text, and why the files are missing.
					r.Files, r.Error, r.ErrorCode = nil, "the reply's files were refused by the hub: "+err.Error(), "files_refused"
					continue
				}
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

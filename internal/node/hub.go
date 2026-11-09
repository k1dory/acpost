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
	// originAcked is a delivered message whose outcome the hub took (or had already): the lease
	// follows. It is still a delivery (it starts with originHub).
	originAcked = originHub + "acked:"

	announceEvery = 20 * time.Second
	announceTTL   = 70 * time.Second
	hubLease      = 2 * time.Minute
	// hubDeliveries is how many messages from the hub a node takes in at once: a delivery
	// downloading its files does not hold up the next ones.
	hubDeliveries = 8
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
			return store.Message{}, false, ctx.Err() // cut short, not "none came"
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
	if err := asListed(listed, files, side); err != nil {
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
	// From a request: a node closing starts no loop (followSweep picks the message up next start).
	if !n.goWork(func() {
		defer func() {
			n.followMu.Lock()
			delete(n.following, id)
			n.followMu.Unlock()
		}()
		n.follow(id)
	}) {
		n.followMu.Lock()
		delete(n.following, id)
		n.followMu.Unlock()
	}
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
				if err == nil && m.State.Final() && !n.copyHubEvents(id) {
					continue // its events first: the next sweep tries again
				}
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

// follow copies the hub's events and outcome of a forwarded message onto its local copy: from the
// hub's event stream, or (a hub without one) by waiting for the outcome.
func (n *Node) follow(id string) {
	m, err := n.followHub(id)
	if err == nil && n.settleFrom(id, m, nil) {
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		return // the local copy is gone
	}
	for n.ctx.Err() == nil {
		wctx, done := context.WithTimeout(n.ctx, client.MaxWait+hubCallTimeout)
		m, err := n.hub.Wait(wctx, id, client.MaxWait)
		done()
		if err == nil && m.State.Final() && !n.copyHubEvents(id) {
			err = errors.New("the hub's events did not come") // again, after the pause below
		} else if n.settleFrom(id, m, err) {
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
			actx, done := context.WithTimeout(n.ctx, hubCallTimeout)
			err := n.hub.Announce(actx, n.Name(), n.announced())
			done()
			if err != nil && n.ctx.Err() == nil {
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
	// Outcomes the last process had not handed the hub for certain: handed again.
	n.goLoop(n.resumeReplies)
	// Messages it stored to send and did not learn the fate of.
	n.goLoop(n.resumeForwards)
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

// hubClaimLoop takes the node's messages from the hub: one long poll while idle, and each message
// delivered on its own (up to hubDeliveries at once) while the next is claimed.
func (n *Node) hubClaimLoop() {
	slots := make(chan struct{}, hubDeliveries)
	for n.ctx.Err() == nil {
		select {
		case slots <- struct{}{}: // a free delivery slot before claiming: nothing is held unrun
		case <-n.ctx.Done():
			return
		}
		cctx, done := context.WithTimeout(n.ctx, hubClaimWait+hubCallTimeout)
		m, ok, err := n.hub.NodeClaim(cctx, n.Name(), hubClaimWait, hubLease)
		done()
		if err != nil || !ok {
			<-slots
		}
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
			n.goLoop(func() {
				defer func() { <-slots }()
				n.deliver(m)
			})
		}
	}
}

// deliver makes a message the hub handed over a local one for the agent it names.
func (n *Node) deliver(m store.Message) {
	ctx := n.ctx
	to, err := ParseAddress(m.To)
	refuse := func(why string) {
		rctx, done := context.WithTimeout(ctx, hubCallTimeout)
		_ = n.hub.Reply(rctx, m.ID, client.ReplyRequest{Lease: m.LeaseID, Error: why})
		done()
	}
	if err != nil || to.Node != n.Name() || n.agentCfg(to.Name) == nil {
		refuse("node " + n.Name() + " has no agent " + m.To)
		return
	}
	if n.cfg.Agents[to.Name] == nil {
		// A cloud sandbox (an agent added as the node's user used it): never through the hub.
		refuse("agent:" + to.Name + " is a cloud sandbox of node " + n.Name() + "'s user")
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
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		var e *Error
		if errors.As(err, &e) {
			refuse(fmt.Sprintf("node %s: %v", n.Name(), err)) // over the limits, not what was listed: final
			return
		}
		if err != nil {
			// The hub or the way to it failing is passing: its lease runs out and it delivers again
			// (an attempt spent, not the message).
			n.log.Warn("delivery's files did not come; left to the next delivery", "id", m.ID, "err", err)
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
	if why, ok := n.takeDelivery(ctx, local, m.Attempts, lim.MaxQueuedPerAgent); !ok {
		if why != "" {
			refuse(why)
		}
		return
	}
	n.notify()
	n.goLoop(func() { n.relay(m.ID, m.LeaseID) })
}

// relay keeps the hub's lease on a delivered message alive while it runs here, hands the hub its
// events as they are recorded, and the outcome when it is final (its events all handed first). A
// message the hub cancelled is cancelled here.
func (n *Node) relay(id, lease string) {
	var sent int64
	pushing, more, extended := true, false, time.Now()
	for n.ctx.Err() == nil {
		evs, changed := n.eventChanges(), n.changes()
		m, err := n.Get(n.ctx, n.LocalUser(), id)
		if err != nil {
			return
		}
		if pushing && m.State.Final() {
			n.relayAll(id, lease, sent, extended) // the events before the reply that ends them
		} else if pushing {
			sent, pushing, more = n.relayEvents(id, lease, sent)
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
			n.replyHub(id, lease, r, extended)
			return
		}
		if m.Origin != originHub+lease {
			return // delivered again under another lease: that delivery's relay takes over
		}
		if time.Since(extended) >= hubRenew {
			extended = time.Now()
			ectx, done := context.WithTimeout(n.ctx, hubCallTimeout)
			err := n.hub.Extend(ectx, id, lease, hubLease)
			done()
			if err != nil && isFinalErr(err) {
				n.hubLeaseGone(id, lease)
				return
			}
		}
		if more {
			continue // more events waiting: on, after the lease above
		}
		select {
		case <-evs:
		case <-changed:
		case <-time.After(hubRenew - time.Since(extended)):
		case <-n.ctx.Done():
			return
		}
	}
}

// takeDelivery makes a delivery the local copy of its message. An earlier delivery of the same
// message is adopted first, in one step with its new lease (a full queue does not refuse what is
// already here); only a new one is admitted under the queue's limit. Not ok: why is the refusal
// for the hub, or "" when the delivery is left to the hub's lease (a passing failure of the store,
// or a newer delivery of the message owns the copy).
//
// attempt is the hub's attempt number of this delivery. Every delivery ends with the adopt that
// records it, a new copy too, so of two deliveries taken at once in any order the newer owns the
// copy at the end: an older one's adopt after it is refused.
func (n *Node) takeDelivery(ctx context.Context, local *store.Message, attempt, maxQueued int) (why string, ok bool) {
	for range 3 {
		_, err := n.st.AdoptDelivery(ctx, local.ID, local.From, local.To, originHub, local.Origin, attempt)
		switch {
		case err == nil:
			return "", true
		case errors.Is(err, store.ErrStaleDelivery):
			// The hub handed the message again since (this lease is no longer its): that
			// delivery's relay hands the outcome.
			n.log.Info("an older delivery came after a newer one; left to the newer", "id", local.ID, "attempt", attempt)
			return "", false
		case errors.Is(err, store.ErrIDInUse):
			// Only an earlier delivery of this same message is adopted, never a local message with the id.
			return "node " + n.Name() + ": message id " + local.ID + " is already in use here", false
		case !errors.Is(err, store.ErrNotFound):
			n.log.Warn("take a delivery", "id", local.ID, "err", err)
			return "", false
		}
		_, _, err = n.st.InsertLimited(ctx, local, maxQueued)
		switch {
		case err == nil:
			continue // stored: the adopt records its attempt
		case errors.Is(err, store.ErrQueueFull):
			return fmt.Sprintf("node %s: %s has a full queue (limits.max_queued_per_agent)", n.Name(), local.To), false
		case errors.Is(err, store.ErrStoreFull):
			return fmt.Sprintf("node %s: %v", n.Name(), err), false
		}
		// Another delivery of it was stored meanwhile (the id is taken): that one is adopted.
	}
	n.log.Warn("take a delivery: the id is taken and not adoptable", "id", local.ID)
	return "", false
}

// hubLeaseGone is what a relay does when the hub no longer takes its lease on a delivered message.
// The hub cancelled it, or ended it without this node (it gave up after the lease was lost too
// often): the copy here ends too, if it is still this delivery's. Otherwise the lease went to
// another delivery, or the hub's lease ran out and it delivers again: the copy is left as it is (a
// turn running goes on) for the next delivery to adopt, and watched until then; it is not
// cancelled for good.
func (n *Node) hubLeaseGone(id, lease string) {
	// The hub's word is needed: without it (the hub did not answer, the store failed) it is asked
	// again, a pause longer each time, until it answers or another delivery owns the copy.
	for pause := hubRetry; n.ctx.Err() == nil; pause = min(2*pause, time.Minute) {
		if cur, err := n.st.Get(n.ctx, id); err == nil && (cur.Origin != originHub+lease || cur.State.Final()) {
			return // adopted by another delivery, or ended here
		} else if errors.Is(err, store.ErrNotFound) {
			return
		}
		if n.hubLeaseVerdict(id, lease) {
			return
		}
		select {
		case <-n.ctx.Done():
			return
		case <-time.After(pause):
		}
	}
}

// hubLeaseVerdict asks the hub once what became of a delivered message whose lease it no longer
// takes, and acts on the answer; it reports whether it had one.
func (n *Node) hubLeaseVerdict(id, lease string) bool {
	ctx, done := context.WithTimeout(n.ctx, hubCallTimeout)
	hm, err := n.hub.Get(ctx, id)
	done()
	var ce *client.Error
	code, why := "", ""
	switch {
	case err == nil && hm.State == store.Cancelled:
	case err == nil && hm.State.Final(), errors.As(err, &ce) && ce.Code == "not_found":
		code, why = store.CodeHubGaveUp, "the hub ended the message without this node"
		if err == nil && hm.Error != "" {
			why += ": " + hm.Error
		}
	default:
		// Not ended there (queued for its next delivery), or no answer: watched on, until a
		// delivery adopts the copy or the hub ends the message (a cancel comes with no delivery).
		return false
	}
	ok, err := n.st.CancelIfOrigin(n.ctx, id, originHub+lease, code, why)
	if err != nil {
		return false
	}
	if ok {
		n.notify()
	}
	return true
}

// replyHub hands the hub a delivered message's outcome under its lease (last renewed at
// extended). It keeps trying while the lease is this delivery's, renewing it between tries, so a
// hub away for a while gets the outcome when it is back instead of reaping the lease and running
// the message again; an outcome taken is marked (originAcked) so a restart does not hand it again.
func (n *Node) replyHub(id, lease string, r client.ReplyRequest, extended time.Time) {
	pause := hubRetry
	for n.ctx.Err() == nil {
		ctx, done := context.WithTimeout(n.ctx, hubReplyTimeout)
		err := n.hub.Reply(ctx, id, r)
		done()
		if err != nil && len(r.Files) > 0 && filesRefused(err) {
			// The hub takes no such files: the sender still gets the text, and why the files are missing.
			r.Files, r.Error, r.ErrorCode = nil, "the reply's files were refused by the hub: "+err.Error(), "files_refused"
			continue
		}
		if err == nil || isFinalErr(err) {
			// Taken, or the hub has an outcome or another delivery of it (which hands its own).
			_, _ = n.st.SetOriginIf(n.ctx, id, originHub+lease, originAcked+lease)
			return
		}
		if cur, err := n.st.Get(n.ctx, id); err != nil || cur.Origin != originHub+lease {
			return // gone, or adopted by another delivery: its relay hands the outcome
		}
		if time.Since(extended) >= hubRenew {
			ectx, edone := context.WithTimeout(n.ctx, hubCallTimeout)
			eerr := n.hub.Extend(ectx, id, lease, hubLease)
			edone()
			if eerr == nil {
				extended = time.Now()
			} else if isFinalErr(eerr) {
				return // the lease is gone: the hub delivers again (that delivery hands the outcome) or gave up
			}
		}
		select {
		case <-n.ctx.Done():
			return
		case <-time.After(pause):
		}
		pause = min(2*pause, hubRenew)
	}
}

// hubReplyWindow is how far back a start looks for outcomes the hub did not confirm: past it the
// hub has delivered the message again or given it up long since.
const hubReplyWindow = 24 * time.Hour

// resumeReplies hands the hub again, a few at once, the outcomes of delivered messages it did not
// confirm taking before the last process stopped.
func (n *Node) resumeReplies() {
	slots := make(chan struct{}, hubDeliveries)
	var wg sync.WaitGroup
	defer wg.Wait()
	since := time.Now().Add(-hubReplyWindow)
	for after := ""; n.ctx.Err() == nil; {
		page, err := n.st.FinalOfOrigin(n.ctx, originHub, originAcked, since, after, store.MaxPage)
		if err != nil || len(page) == 0 {
			return
		}
		for _, it := range page {
			id, lease := it[0], strings.TrimPrefix(it[1], originHub)
			select {
			case slots <- struct{}{}:
			case <-n.ctx.Done():
				return
			}
			wg.Add(1)
			go func() {
				defer func() { <-slots; wg.Done() }()
				n.relay(id, lease)
			}()
		}
		after = page[len(page)-1][0]
	}
}

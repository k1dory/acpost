// Package node is what `acpost serve` runs: mailboxes over a store, the drivers that serve them,
// who may do what, and the link to a hub.
package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// Version is set at build time.
var Version = "dev"

// maxError bounds the text of a failure.
const maxError = 16 << 10

// maxLabel bounds a task, a thread, an idempotency key or a session id: labels, not content.
const maxLabel = 1024

const truncatedMark = "\n…[truncated by acpost: limits.max_reply]"

// Principal is who makes a request.
type Principal struct {
	Kind  string // user, agent, client, node
	Name  string
	Token *config.Token
	agent string // an agent process's token: its current message is the parent of what it sends
}

// Address is the principal's address as a sender.
func (p Principal) Address() string { return p.Kind + ":" + p.Name }

// local says whether the principal is the node's own user or one of its agents: they see and do
// everything on the node.
func (p Principal) local() bool { return p.Kind == "user" || p.Kind == "agent" }

// SendRequest is a message to send.
type SendRequest struct {
	To, Body, Task, Thread string
	Meta                   map[string]string
	// Session continues an existing ACP session of the agent (one opened in an editor, say).
	Session string
	// Cwd, Model, Effort, Mode: a new task's directory and the session's settings for the message.
	Cwd, Model, Effort, Mode string
	// ModeStrict: the turn runs only in Mode (it fails if the agent cannot set it).
	ModeStrict bool
	// Files go with the message (checked against limits.max_files and max_files_size).
	Files   []store.File
	TTL     time.Duration
	IdemKey string
	// Honoured only from a node principal (a node forwarding its own message to its hub).
	ID, From, Parent string
	Depth            int
}

// Node is a running node.
type Node struct {
	cfg   *config.Config
	st    *store.Store
	log   *slog.Logger
	login string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// changed is closed and replaced whenever a message changes: waiters and claimers re-check.
	mu        sync.Mutex
	changed   chan struct{}
	evChanged chan struct{} // closed when an event is recorded: followers read on
	terms     terms         // the user's terminals
	forges    forges        // forges of agents' repositories
	asks      asks          // permission requests waiting for a person

	tokMu   sync.Mutex
	agentTk map[string]*agentToken // token → agent process

	acp       map[string]*acpAgent
	sandboxes map[string]*sandboxAgent // driver: sandbox, cloud agents (the cloud's added as used: sbMu)
	sbMu      sync.Mutex
	schedBusy atomic.Bool              // a schedules pass is running
	workMu    sync.RWMutex             // goWork against Close
	chatList  chatCache                // sessions and sandboxes listed elsewhere, cached
	dynCfg    map[string]*config.Agent // the cloud's sandboxes, as agents
	cloud     *cloudAPI
	started   bool // the drivers run (sbMu): a cloud sandbox's agent starts when added
	mcpSec    mcpSecrets

	maintainEvery atomic.Int64
	wake          chan struct{}

	hub    *client.Client // the hub this node joined
	remote *registry      // on a hub: the agents nodes announced

	followMu   sync.Mutex
	following  map[string]bool // forwarded messages with a long poll of their own
	followWake chan struct{}

	// turns holds the copies of attached files an agent reads during its turn.
	turns string

	closed atomic.Bool

	held      heldLeases // the leases of turns running in this process (lease.go)
	cloudOps  sync.Map   // idempotency key → *sync.Mutex: one cloud task made at a time per key (cloud.go)
	resolving sync.Map   // ids of forwarded messages resolveForward runs for (forward.go)
}

type agentToken struct {
	agent   string
	current atomic.Value // turnRef: the message the agent process is working on, and the attempt's lease
}

type turnRef struct{ id, lease string }

// StoreOptions are the store limits a configuration sets.
func StoreOptions(cfg *config.Config) store.Options {
	return store.Options{MaxStore: int64(cfg.Limits.MaxStore), MaxEventsPerMessage: cfg.Limits.MaxEventsPerMessage}
}

// New makes a node over an open store. log may be nil.
func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*Node, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	n := &Node{cfg: cfg, st: st, log: log, changed: make(chan struct{}), evChanged: make(chan struct{}), agentTk: map[string]*agentToken{},
		acp: map[string]*acpAgent{}, sandboxes: map[string]*sandboxAgent{}, dynCfg: map[string]*config.Agent{}, wake: make(chan struct{}, 1), remote: newRegistry(),
		following: map[string]bool{}, followWake: make(chan struct{}, 1)}
	n.maintainEvery.Store(int64(30 * time.Second))
	n.login = "user"
	if u, err := user.Current(); err == nil && config.ValidName(u.Username) {
		n.login = u.Username
	}
	for name, a := range cfg.Agents {
		switch a.Driver {
		case "acp":
			n.acp[name] = newACPAgent(n, name, a)
		case "sandbox":
			sa, err := newSandboxAgent(n, name, a)
			if err != nil {
				return nil, err
			}
			n.sandboxes[name] = sa
		}
	}
	if cfg.Cloud != nil {
		c, err := newCloudAPI(cfg.Cloud)
		if err != nil {
			return nil, err
		}
		n.cloud = c
	}
	if j := cfg.Node.Join; j != nil {
		if j.Token == "" {
			// Only the node needs the hub's token; the CLI reads the same file without it.
			return nil, fmt.Errorf("node.join: %s is not set in this process's environment: the node needs the hub's token to join", j.TokenEnv)
		}
		c, err := client.New(j.URL, j.Token, j.CA)
		if err != nil {
			return nil, fmt.Errorf("node.join: %w", err)
		}
		// What the hub sends is read within what this node would take: a message carries its body and
		// reply, never file data (files come by SideFiles, under fileResponse).
		l := cfg.Limits
		c.MaxResponse = 6*(int64(l.MaxBody)+int64(l.MaxReply)+int64(l.MaxMeta)+4*maxLabel+maxError) +
			// and the listing of both sides' files: names up to 255 bytes, types up to 127, as JSON
			int64(2*l.MaxFiles)*(6*(255+127)+128) + 1<<20
		n.hub = c
	}
	return n, nil
}

// Start requeues the turns a previous process left running, starts the agents' drivers, the
// maintenance loop and, when joined, the link to the hub.
func (n *Node) Start(ctx context.Context) error {
	n.ctx, n.cancel = context.WithCancel(context.WithoutCancel(ctx))
	if err := n.openTurns(); err != nil {
		return err
	}
	var mine []string
	for name := range n.acp {
		mine = append(mine, "agent:"+name)
	}
	n.sbMu.Lock()
	for name := range n.sandboxes {
		mine = append(mine, "agent:"+name)
	}
	n.sbMu.Unlock()
	// The cloud's sandboxes with messages: their agents again, before their turns are requeued.
	if n.cloud != nil {
		names, err := n.st.ActiveRecipients(ctx, "agent:sb_")
		if err != nil {
			return err
		}
		for _, to := range names {
			if name := strings.TrimPrefix(to, "agent:"); config.ValidName(name) {
				if _, err := n.cloudAgent(name, false); err == nil {
					mine = append(mine, to)
				}
			}
		}
	}
	if k, err := n.st.RequeueRunning(ctx, mine, n.cfg.Limits.MaxAttempts); err != nil {
		return err
	} else if k > 0 {
		n.log.Info("requeued turns interrupted by the last stop", "messages", k)
	}
	for _, a := range n.acp {
		a.start()
	}
	n.sbMu.Lock()
	for _, a := range n.sandboxes {
		a.start()
	}
	n.started = true
	n.sbMu.Unlock()
	n.goLoop(n.maintainLoop)
	if n.hub != nil {
		n.startJoin()
	}
	return nil
}

func (n *Node) goLoop(fn func()) {
	n.wg.Add(1)
	go func() { defer n.wg.Done(); fn() }()
}

// goWork starts fn as a loop Close waits for, unless the node is closing: false then. Unlike
// goLoop it may be called from anywhere (a request), not only from a loop Close already waits for.
func (n *Node) goWork(fn func()) bool {
	n.workMu.RLock()
	defer n.workMu.RUnlock()
	if n.closed.Load() {
		return false
	}
	n.goLoop(fn)
	return true
}

// Close stops the drivers (their agent processes too) and the loops. Messages in flight stay
// running in the store and are requeued by the next start. The store is the caller's to close.
func (n *Node) Close() {
	n.workMu.Lock() // no goWork begins past this point
	first := n.closed.CompareAndSwap(false, true)
	n.workMu.Unlock()
	if !first || n.cancel == nil {
		return
	}
	n.cancel()
	n.askPending().Close() // nobody answers any more: requests waiting are refused
	for _, a := range n.acp {
		a.stop()
	}
	n.wg.Wait()
	if n.st.Path() == "" {
		_ = os.RemoveAll(n.turns)
	}
}

// LocalURL is the address the node's own agents reach it at.
func (n *Node) LocalURL() string {
	for _, l := range n.cfg.Node.Listen {
		if strings.HasPrefix(l, "unix://") {
			return l
		}
	}
	for _, l := range n.cfg.Node.Listen {
		if n.cfg.Node.TLS == nil {
			return "http://" + strings.TrimPrefix(l, "tcp://")
		}
		return "https://" + strings.TrimPrefix(l, "tcp://")
	}
	return ""
}

// Name is the node's name.
func (n *Node) Name() string { return n.cfg.Node.Name }

// LocalUser is the principal of a request on the Unix socket without a token.
func (n *Node) LocalUser() Principal { return Principal{Kind: "user", Name: n.login} }

func (n *Node) setMaintainEvery(d time.Duration) {
	n.maintainEvery.Store(int64(d))
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// notify wakes everyone waiting for a change.
func (n *Node) notify() {
	n.mu.Lock()
	close(n.changed)
	n.changed = make(chan struct{})
	n.mu.Unlock()
}

func (n *Node) changes() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.changed
}

func randomToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "acpa_" + hex.EncodeToString(b[:])
}

func (n *Node) issueAgentToken(agent string) string {
	t := randomToken()
	at := &agentToken{agent: agent}
	at.current.Store(turnRef{})
	n.tokMu.Lock()
	n.agentTk[t] = at
	n.tokMu.Unlock()
	return t
}

// revokeAgentToken ends a token issued for a process that is gone.
func (n *Node) revokeAgentToken(token string) {
	n.tokMu.Lock()
	delete(n.agentTk, token)
	n.tokMu.Unlock()
}

func (n *Node) setCurrent(token, msgID, lease string) {
	n.tokMu.Lock()
	at := n.agentTk[token]
	n.tokMu.Unlock()
	if at != nil {
		at.current.Store(turnRef{msgID, lease})
	}
}

// PrincipalForToken resolves a bearer token: an agent process's, or a configured one.
func (n *Node) PrincipalForToken(tok string) (Principal, bool) {
	if tok == "" {
		return Principal{}, false
	}
	n.tokMu.Lock()
	at := n.agentTk[tok]
	n.tokMu.Unlock()
	if at != nil {
		return Principal{Kind: "agent", Name: at.agent, agent: tok}, true
	}
	for _, t := range n.cfg.Tokens {
		if t.Matches(tok) {
			if t.Node {
				return Principal{Kind: "node", Name: t.Name, Token: t}, true
			}
			return Principal{Kind: "client", Name: t.Name, Token: t}, true
		}
	}
	return Principal{}, false
}

// currentOf is the message the agent process p works on.
func (n *Node) currentOf(p Principal) string { return n.turnOf(p).id }

// turnOf is the message the agent process p works on, with the lease of its attempt.
func (n *Node) turnOf(p Principal) turnRef {
	n.tokMu.Lock()
	at := n.agentTk[p.agent]
	n.tokMu.Unlock()
	if at == nil || at.agent != p.Name {
		return turnRef{}
	}
	return at.current.Load().(turnRef)
}

func (n *Node) agentCfg(name string) *config.Agent {
	if a := n.cfg.Agents[name]; a != nil {
		return a
	}
	n.sbMu.Lock()
	defer n.sbMu.Unlock()
	return n.dynCfg[name]
}

// Send stores a message for an agent of this node, or hands it to the hub for an agent elsewhere.
func (n *Node) Send(ctx context.Context, p Principal, r SendRequest) (store.Message, bool, error) {
	to, err := ParseAddress(r.To)
	if err != nil {
		return store.Message{}, false, errf("invalid", "%v", err)
	}
	if to.Kind != "agent" {
		return store.Message{}, false, errf("invalid", "%s: messages go to agents (agent:name)", r.To)
	}
	// A cloud sandbox of the tenant is the node's user's (its agents' too): never a client's or
	// another node's, registered or not. Its agent is added on first use.
	if (to.Node == "" || to.Node == n.Name()) && n.cloud != nil && strings.HasPrefix(to.Name, "sb_") && n.cfg.Agents[to.Name] == nil {
		if !p.local() {
			return store.Message{}, false, errf("refused", "a cloud sandbox is reached by the node's user")
		}
		if _, err := n.cloudAgent(to.Name, true); err != nil {
			return store.Message{}, false, err
		}
	}
	if r.Body == "" && len(r.Files) == 0 {
		return store.Message{}, false, errf("invalid", "the body is empty")
	}
	if r.TTL < 0 {
		return store.Message{}, false, errf("invalid", "ttl is negative")
	}
	lim := n.cfg.Limits
	if len(r.Body) > int(lim.MaxBody) {
		return store.Message{}, false, errf("refused", "the body is %d bytes, over limits.max_body (%d)", len(r.Body), lim.MaxBody)
	}
	switch {
	case len(r.Task) > maxLabel:
		return store.Message{}, false, errf("invalid", "the task is %d bytes, over %d", len(r.Task), maxLabel)
	case len(r.Thread) > maxLabel:
		return store.Message{}, false, errf("invalid", "the thread is %d bytes, over %d", len(r.Thread), maxLabel)
	case len(r.IdemKey) > maxLabel:
		return store.Message{}, false, errf("invalid", "the idempotency key is %d bytes, over %d", len(r.IdemKey), maxLabel)
	case len(r.Session) > maxLabel:
		return store.Message{}, false, errf("invalid", "the session is %d bytes, over %d", len(r.Session), maxLabel)
	}
	if ms := metaSize(r.Meta); ms > int(lim.MaxMeta) {
		return store.Message{}, false, errf("refused", "meta is %d bytes, over limits.max_meta (%d)", ms, lim.MaxMeta)
	}
	if err := n.checkFiles(r.Files, nil, "the message"); err != nil {
		return store.Message{}, false, err
	}

	// A repeat (same sender, same key) returns the first message before anything is resolved again.
	if r.IdemKey != "" && p.Kind != "node" {
		if prev, err := n.st.ByIdem(ctx, p.Address(), r.IdemKey); err == nil && prev.Origin != originUnsent {
			// (unsent: its key was released between the lookup and the read; it is sent anew)
			if prev.Origin == originForwarding && !prev.State.Final() {
				return store.Message{}, false, pendingErr(prev.ID) // not known to be sent: no success yet
			}
			return hideLease(prev), false, nil
		}
	}
	if err := n.resolveSession(ctx, p, to, &r); err != nil {
		return store.Message{}, false, err
	}
	if err := n.resolveSettings(ctx, p, to, &r); err != nil {
		return store.Message{}, false, err
	}
	m := &store.Message{ID: store.NewID(), From: p.Address(), Body: r.Body, Task: r.Task, Thread: r.Thread, Meta: r.Meta, IdemKey: r.IdemKey, Files: r.Files}
	if r.TTL > 0 {
		m.ExpiresAt = time.Now().Add(r.TTL)
	}
	switch p.Kind {
	case "agent":
		// The agent's current message is the parent: the chain is recorded and bounded.
		if parentID := n.currentOf(p); parentID != "" {
			if parent, err := n.st.Get(ctx, parentID); err == nil {
				m.Parent, m.Depth = parent.ID, parent.Depth+1
				if m.Task == "" {
					m.Task = parent.Task
				}
			}
		}
	case "client":
		if !matchAny(p.Token.SendTo, to.String()) && !matchAny(p.Token.SendTo, "agent:"+to.Name) {
			return store.Message{}, false, errf("refused", "token %s may not send to %s", p.Name, to)
		}
	case "node":
		// A node forwards a message of its own: it names the sender, which must be one of its own.
		if r.From != "" {
			from, err := ParseAddress(r.From)
			if err != nil || from.Node != p.Name {
				return store.Message{}, false, errf("refused", "node %s may only send as its own addresses (…@%s)", p.Name, p.Name)
			}
			m.From = from.String()
		} else {
			m.From = "node:" + p.Name
		}
		if r.ID != "" {
			if !strings.HasPrefix(r.ID, "msg_") || len(r.ID) != 30 {
				return store.Message{}, false, errf("invalid", "bad message id %q", r.ID)
			}
			m.ID = r.ID
			if m.IdemKey == "" {
				m.IdemKey = "fwd:" + r.ID // a repeated forward returns the first
			}
		}
		m.Parent, m.Depth = r.Parent, r.Depth
	}
	if m.Depth > lim.MaxDepth {
		return store.Message{}, false, errf("refused", "the chain of messages is %d deep, over limits.max_depth (%d)", m.Depth, lim.MaxDepth)
	}

	// Where does it go?
	local := (to.Node == "" || to.Node == n.Name()) && n.agentCfg(to.Name) != nil
	if local && p.Kind == "agent" && p.Name == to.Name {
		if err := n.checkSelfSend(ctx, m, to.Name, n.agentCfg(to.Name)); err != nil {
			return store.Message{}, false, err
		}
	}
	switch {
	case local:
		m.To = "agent:" + to.Name
		if p.Kind == "node" {
			if err := noLocalLinks(m.Files, m.To, sendCopyHint); err != nil {
				return store.Message{}, false, err
			}
		}
		a := n.agentCfg(to.Name)
		if !matchAny(a.AllowFrom, m.From) {
			return store.Message{}, false, errf("refused", "%s does not take messages from %s (allow_from)", m.To, m.From)
		}
	case n.remote.has(to):
		// This node is the hub and a joined node serves the agent.
		node, err := n.remote.resolve(to)
		if err != nil {
			return store.Message{}, false, err
		}
		m.To = "agent:" + to.Name + "@" + node
		if err := noLocalLinks(m.Files, m.To, sendCopyHint); err != nil {
			return store.Message{}, false, err
		}
	case n.hub != nil && p.Kind != "node":
		if err := noLocalLinks(m.Files, to.String(), sendCopyHint); err != nil {
			return store.Message{}, false, err
		}
		return n.forward(ctx, m, to)
	default:
		return store.Message{}, false, errh("not_found", "`acpost agents` lists the agents this node knows", "no agent %s here", to)
	}
	got, created, err := n.st.InsertLimited(ctx, m, lim.MaxQueuedPerAgent)
	if errors.Is(err, store.ErrQueueFull) {
		return got, false, errf("refused", "%s has %d messages queued (limits.max_queued_per_agent)", m.To, lim.MaxQueuedPerAgent)
	}
	if err != nil {
		return got, false, n.fileErr(err, m.ID)
	}
	if created {
		n.notify()
	}
	return hideLease(got), created, nil
}

func metaSize(meta map[string]string) int {
	n := 0
	for k, v := range meta {
		n += len(k) + len(v)
	}
	return n
}

// capReply cuts a reply to max bytes, marking the cut when there is room for the mark.
func capReply(reply string, max int) string {
	if len(reply) <= max {
		return reply
	}
	if max <= len(truncatedMark) {
		return reply[:max]
	}
	return reply[:max-len(truncatedMark)] + truncatedMark
}

// hideLease is a message as others see it: no lease (only its claimer has it), and the hint of its error.
func hideLease(m store.Message) store.Message {
	m.LeaseID, m.LeaseUntil = "", time.Time{}
	return withHint(m)
}

// canSee says whether p may read m.
func (n *Node) canSee(p Principal, m store.Message) bool {
	switch p.Kind {
	case "user", "agent":
		return true
	case "client":
		return m.From == p.Address() || slices.ContainsFunc(p.Token.ActAs, func(a string) bool { return m.To == "agent:"+a })
	case "node":
		return strings.HasSuffix(m.To, "@"+p.Name) || strings.HasSuffix(m.From, "@"+p.Name)
	}
	return false
}

// canServe says whether p may take and answer messages addressed to recipient.
func (n *Node) canServe(p Principal, recipient string) bool {
	switch p.Kind {
	case "user", "agent":
		return true
	case "client":
		return slices.ContainsFunc(p.Token.ActAs, func(a string) bool { return recipient == "agent:"+a })
	case "node":
		return strings.HasSuffix(recipient, "@"+p.Name)
	}
	return false
}

// Get returns a message p may see.
func (n *Node) Get(ctx context.Context, p Principal, id string) (store.Message, error) {
	m, err := n.st.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !n.canSee(p, m)) {
		return store.Message{}, errf("not_found", "no message %s", id)
	}
	return hideLease(m), err
}

// Wait returns the message once it is final, or as it is when timeout passes.
func (n *Node) Wait(ctx context.Context, p Principal, id string, timeout time.Duration) (store.Message, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		ch := n.changes()
		m, err := n.Get(ctx, p, id)
		if err != nil || m.State.Final() {
			return m, err
		}
		select {
		case <-ch:
		case <-time.After(2 * time.Second): // a change made by another path (shadow sync) is seen anyway
		case <-deadline.C:
			return n.Get(ctx, p, id)
		case <-ctx.Done():
			return m, ctx.Err()
		}
	}
}

// List returns a page of messages p may see.
func (n *Node) List(ctx context.Context, p Principal, f store.Filter) ([]store.Message, string, error) {
	switch p.Kind {
	case "client":
		f.Party = clientParty(p)
	case "node":
		return nil, "", errf("refused", "a node does not list messages")
	}
	if f.State != "" && !f.State.Valid() {
		return nil, "", errf("invalid", "state %q", f.State)
	}
	list, next, err := n.st.List(ctx, f)
	for i := range list {
		list[i] = withHint(list[i])
	}
	return list, next, err
}

// Conversations lists threads p may see, the most recently active first.
func (n *Node) Conversations(ctx context.Context, p Principal, before string, limit int) ([]store.Conversation, string, error) {
	var party *store.Party
	switch p.Kind {
	case "client":
		party = clientParty(p)
	case "node":
		return nil, "", errf("refused", "a node does not list messages")
	}
	out, next, err := n.st.Conversations(ctx, party, before, limit)
	if err != nil {
		return nil, "", errf("invalid", "%v", err)
	}
	return out, next, nil
}

// Events returns a page of what happened while a message was handled.
func (n *Node) Events(ctx context.Context, p Principal, id string, after int64, limit int) ([]store.Event, error) {
	if _, err := n.Get(ctx, p, id); err != nil {
		return nil, err
	}
	return n.st.Events(ctx, id, after, limit)
}

// Cancel ends a message that is not final. Its sender, the node's user, or whoever serves its
// recipient may cancel it.
func (n *Node) Cancel(ctx context.Context, p Principal, id string) (store.Message, error) {
	m, err := n.Get(ctx, p, id)
	if err != nil {
		return m, err
	}
	nodeOwn := p.Kind == "node" && strings.HasSuffix(m.From, "@"+p.Name)
	if !p.local() && !nodeOwn && m.From != p.Address() && !n.canServe(p, m.To) {
		return m, errf("refused", "only the sender may cancel %s", id)
	}
	afterCancelRead(id)
	if m.Origin == originForwarding {
		cur, done, err := n.cancelForwarding(ctx, p, m)
		if err != nil || done {
			return cur, err
		}
		m = cur // the send finished meanwhile: as it is now
	}
	if m.Origin == originRemote {
		// The hub owns the outcome: if it already has one, that is what the local copy gets (follow).
		if _, err := n.hub.Cancel(ctx, id); err != nil {
			if isFinalErr(err) {
				n.wakeFollow()
				return m, errf("conflict", "%s already ended on the hub", id)
			}
			return m, hubErr(err)
		}
	}
	got, err := n.st.Cancel(ctx, id)
	if errors.Is(err, store.ErrFinal) {
		return hideLease(got), errf("conflict", "%s is already %s", id, got.State)
	}
	if err != nil {
		return got, err
	}
	n.notify()
	return hideLease(got), nil
}

// EditQueued changes what a queued message asks, before its agent takes it: the node's own user or
// agents, of a message sent here (one gone through the hub, or come from it, is the hub's).
func (n *Node) EditQueued(ctx context.Context, p Principal, id, body string) (store.Message, error) {
	if !p.local() {
		return store.Message{}, errf("refused", "only this node's own may edit a queued message")
	}
	m, err := n.Get(ctx, p, id)
	if err != nil {
		return m, err
	}
	if p.Kind != "user" && m.From != p.Address() {
		return m, errf("refused", "only the sender may edit %s", id)
	}
	// A message gone through the hub, come from it, or sent from another node keeps its body where
	// its sender reads it: only one sent here, to be run here, changes.
	if from, err := ParseAddress(m.From); m.Origin != "" || err != nil || from.Node != "" {
		return m, errf("unsupported", "%s is not only this node's: cancel it and send again", id)
	}
	switch {
	case strings.TrimSpace(body) == "":
		return m, errf("invalid", "an empty body")
	case len(body) > int(n.cfg.Limits.MaxBody):
		return m, errf("refused", "the body is %d bytes, over limits.max_body (%d)", len(body), n.cfg.Limits.MaxBody)
	}
	got, err := n.st.EditQueued(ctx, id, body)
	if errors.Is(err, store.ErrFinal) {
		return hideLease(got), errf("conflict", "%s was already taken (%s)", id, got.State)
	}
	if err != nil {
		return got, errf("unavailable", "%v", err)
	}
	n.notify()
	return hideLease(got), nil
}

// Claim takes the next message for an inbox agent, waiting up to wait for one.
func (n *Node) Claim(ctx context.Context, p Principal, agent string, wait, lease time.Duration) (store.Message, bool, error) {
	a := n.agentCfg(agent)
	if a == nil {
		return store.Message{}, false, errh("not_found", "`acpost agents` lists the agents this node knows", "no agent %s", agent)
	}
	if a.Driver != "inbox" {
		return store.Message{}, false, errf("invalid", "agent:%s is served by its %s driver, not taken by hand", agent, a.Driver)
	}
	recipient := "agent:" + agent
	if !n.canServe(p, recipient) {
		return store.Message{}, false, errf("refused", "%s may not act as %s", p.Address(), recipient)
	}
	if lease <= 0 {
		lease = a.Lease.D()
	}
	return n.claim(ctx, []string{recipient}, wait, lease)
}

func (n *Node) claim(ctx context.Context, recipients []string, wait, lease time.Duration) (store.Message, bool, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		ch := n.changes()
		m, ok, err := n.st.ClaimAny(ctx, recipients, lease)
		if err != nil || ok {
			if ok {
				n.notify()
			}
			return m, ok, err
		}
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
		case <-deadline.C:
			return store.Message{}, false, nil
		case <-ctx.Done():
			return store.Message{}, false, nil
		}
	}
}

// Reply answers a message taken under lease: body is the reply, or errText says why there is none.
func (n *Node) Reply(ctx context.Context, p Principal, id, lease, body, errText string) error {
	return n.ReplyCoded(ctx, p, id, lease, body, "", errText, nil)
}

// ReplyCoded is Reply with the failure's code (a joined node passes on its agent's; "" is "failed").
// files go with the reply.
func (n *Node) ReplyCoded(ctx context.Context, p Principal, id, lease, body, errCode, errText string, files []store.File) error {
	m, err := n.st.Get(ctx, id)
	if err != nil {
		return errf("not_found", "no message %s", id)
	}
	if !n.canServe(p, m.To) {
		return errf("refused", "%s may not answer for %s", p.Address(), m.To)
	}
	state := store.Done
	if len(errText) > maxError {
		errText = strings.ToValidUTF8(errText[:maxError], "") + "…"
	}
	if errText != "" {
		state = store.Failed
		if _, known := hints[errCode]; !known {
			errCode = "failed"
		}
	} else {
		errCode = ""
	}
	if err := n.checkFiles(files, replyFiles(m.Files), "the reply"); err != nil {
		return err
	}
	// A joined node answering for its agent is another machine, whoever sent the message.
	if p.Kind == "node" || n.senderElsewhere(m) {
		if err := noLocalLinks(files, m.From, sendCopyHint); err != nil {
			return err
		}
	}
	body = capReply(body, int(n.cfg.Limits.MaxReply))
	if err := n.st.Complete(ctx, id, lease, state, body, errCode, errText, files...); err != nil {
		if errors.Is(err, store.ErrLeaseLost) {
			return errf("conflict", "%v", err)
		}
		return n.fileErr(err, id)
	}
	n.notify()
	return nil
}

// Extend makes a claim last d longer.
func (n *Node) Extend(ctx context.Context, p Principal, id, lease string, d time.Duration) error {
	m, err := n.st.Get(ctx, id)
	if err != nil {
		return errf("not_found", "no message %s", id)
	}
	if !n.canServe(p, m.To) {
		return errf("refused", "%s may not act for %s", p.Address(), m.To)
	}
	if err := n.st.Extend(ctx, id, lease, d); errors.Is(err, store.ErrLeaseLost) {
		return errf("conflict", "%v", err)
	} else {
		return err
	}
}

// Agents lists the agents this node knows: its own and, on a hub, those joined nodes announced; a
// joined node adds what its hub lists of other nodes (a hub that does not answer soon is left out).
func (n *Node) Agents(ctx context.Context) []client.AgentInfo {
	var out []client.AgentInfo
	for name, a := range n.cfg.Agents {
		info := client.AgentInfo{Name: name, Address: "agent:" + name, Driver: a.Driver, Node: n.Name()}
		info.Queued, info.Running, _ = n.st.Counts(ctx, info.Address)
		if d := n.acp[name]; d != nil {
			info.Process = d.running()
		}
		n.sbMu.Lock()
		d := n.sandboxes[name]
		n.sbMu.Unlock()
		if d != nil {
			info.Process = d.busy.Load() // a turn of its sandbox's conversation runs
		}
		out = append(out, info)
	}
	for _, r := range n.remote.list() {
		info := client.AgentInfo{Name: r.agent, Address: "agent:" + r.agent + "@" + r.node, Driver: "remote", Node: r.node}
		info.Queued, info.Running, _ = n.st.Counts(ctx, info.Address)
		out = append(out, info)
	}
	if n.hub != nil {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		elsewhere, err := n.hub.Agents(hctx)
		cancel()
		for _, info := range elsewhere {
			if err == nil && info.Node != n.Name() && info.Node != "" {
				if !strings.Contains(info.Address, "@") {
					info.Address += "@" + info.Node // the hub's own agent
				}
				info.Driver, info.Process = "remote", false
				out = append(out, info)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

func (n *Node) agentRunning(name string) bool {
	if d := n.acp[name]; d != nil {
		return d.running()
	}
	return false
}

// Status is the node's health.
func (n *Node) Status(p Principal) client.Status {
	l := n.cfg.Limits
	s := client.Status{OK: true, Node: n.Name(), Version: Version, You: p.Address(),
		Limits: &client.Limits{MaxBody: int64(l.MaxBody), MaxFiles: l.MaxFiles, MaxFilesSize: int64(l.MaxFilesSize)}}
	if n.hub != nil {
		s.Hub = n.hub.URL
	}
	return s
}

func (n *Node) maintainLoop() {
	lastGC := time.Time{}
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-time.After(time.Duration(n.maintainEvery.Load())):
		case <-n.wake:
			continue
		}
		ctx, now := n.ctx, time.Now()
		n.runSchedules(ctx, now)
		changed := 0
		if k, err := n.st.Expire(ctx, now); err == nil {
			changed += k
		}
		n.held.renew(ctx, n.st) // a turn running here is alive, however long this process stood still
		if k, err := n.st.ReapLeases(ctx, now, n.cfg.Limits.MaxAttempts); err == nil {
			changed += k
		}
		if changed > 0 {
			n.notify()
		}
		if now.Sub(lastGC) > 10*time.Minute {
			lastGC = now
			if k, err := n.st.GC(ctx, now.Add(-n.cfg.Retention.D())); err != nil {
				n.log.Warn("gc", "err", err)
			} else if k > 0 {
				n.log.Info("gc", "deleted", k)
			}
		}
		// Free pages back to the file system, WAL folded in and truncated: cheap when idle.
		if err := n.st.Maintain(ctx); err != nil && ctx.Err() == nil {
			n.log.Warn("store maintenance", "err", err)
		}
	}
}

// clientParty is what a client token may see (as canSee has it): what it sent, and what went to
// the agents it acts as.
func clientParty(p Principal) *store.Party {
	party := &store.Party{Sender: p.Address()}
	for _, a := range p.Token.ActAs {
		party.Recipients = append(party.Recipients, "agent:"+a)
	}
	return party
}

// MessageByIdem returns the message a joined node's sender sent here with an idempotency key: the
// node asks when a send's answer was lost and the hub kept the message under an earlier send's id.
func (n *Node) MessageByIdem(ctx context.Context, p Principal, from, key string) (store.Message, error) {
	if p.Kind != "node" {
		return store.Message{}, errf("refused", "only a joined node looks a message up by its key")
	}
	a, err := ParseAddress(from)
	if err != nil || a.Node != p.Name || key == "" {
		return store.Message{}, errf("refused", "node %s may only look up its own senders' messages (…@%s), by a key", p.Name, p.Name)
	}
	m, err := n.st.ByIdem(ctx, a.String(), key)
	if errors.Is(err, store.ErrNotFound) {
		return store.Message{}, errf("not_found", "no message of %s with that key", a)
	}
	if err != nil {
		return store.Message{}, err
	}
	return hideLease(m), nil
}

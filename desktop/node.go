//go:build darwin

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/forge"
	"github.com/k1dory/acpost/internal/store"
	"github.com/k1dory/acpx"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Node is the app's link to the local acpost node: what the interface calls (bindings) and the
// streams it follows (events "events:<message id>", "term:<terminal id>").
type Node struct {
	app *application.App
	win *application.WebviewWindow // the app's window (the preview is laid over it)

	mu       sync.Mutex
	c        *client.Client
	url      string
	follows  map[string]*follow
	gens     map[string]*followGens // each followed message's newest and let-go generations
	terms    map[string]*term
	stopNode context.CancelFunc // the node the app runs itself, if it does
	nodeDone chan struct{}      // closed when that node has stopped
	nodeOK   bool               // that node got ready (else it is starting or stopping)
	startMu  sync.Mutex         // one ensureNode at a time
	closed   bool               // the app is quitting: no node starts, no terminal opens
	opening  sync.WaitGroup     // terminals being opened
	notes    notifier
	noteOK   int // 0 not asked, 1 allowed, -1 refused
	noteAsk  sync.Once
	startErr string
	emitFn   func(name string, data any) // tests: where events go instead of the window
}

// ServiceStartup connects to the node in the background: the window opens meanwhile (a node
// that is slow to start held it for up to 20 s), and Status waits for the outcome.
func (n *Node) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	n.follows = map[string]*follow{}
	n.terms = map[string]*term{}
	n.startErr = "starting" // Status goes through ensureNode, which waits for this start
	go n.ensureNode()
	return nil
}

// ServiceShutdown stops the streams, closes the terminals the app opened (a node of another
// machine kept their shells) and stops the app's own node, waiting for it to clean up (its
// socket, its pid file). No node starts after it.
func (n *Node) ServiceShutdown() error {
	n.mu.Lock()
	n.closed = true
	for _, f := range n.follows {
		f.cancel()
	}
	var terms []string
	for id, t := range n.terms {
		t.stop()
		terms = append(terms, id)
	}
	clear(n.terms)
	stop, done := n.stopNode, n.nodeDone
	n.mu.Unlock()
	opened := make(chan struct{})
	go func() { n.opening.Wait(); close(opened) }()
	select { // a terminal opening now closes itself when its id comes: waited for as long as an open may take
	case <-opened:
	case <-time.After(termOpenTimeout + 3*time.Second):
	}
	if len(terms) > 0 {
		closeTerminals(n, terms)
	}
	if stop != nil {
		stop()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

// client is the node's client: ACPOST_NODE (and ACPOST_TOKEN) when set, else the configuration's
// local address (the Unix socket), as the command line finds it.
func (n *Node) client() (*client.Client, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.c != nil {
		return n.c, nil
	}
	url, token := os.Getenv("ACPOST_NODE"), os.Getenv("ACPOST_TOKEN")
	if url == "" {
		cfg, err := config.Load(config.DefaultPath())
		if err != nil {
			return nil, fmt.Errorf("the node's configuration: %w", err)
		}
		url = localURL(cfg)
	}
	c, err := client.New(url, token, os.Getenv("ACPOST_CA"))
	if err != nil {
		return nil, err
	}
	n.c, n.url = c, url
	return c, nil
}

func localURL(cfg *config.Config) string {
	for _, l := range cfg.Node.Listen {
		if strings.HasPrefix(l, "unix://") {
			return l
		}
	}
	scheme := "http://"
	if cfg.Node.TLS != nil {
		scheme = "https://"
	}
	return scheme + strings.TrimPrefix(cfg.Node.Listen[0], "tcp://")
}

func call[T any](n *Node, fn func(ctx context.Context, c *client.Client) (T, error)) (T, error) {
	return callLong(n, 30*time.Second, fn)
}

// withHint hands the interface the node's error whole — its code, message and hint (what to do
// next), as JSON — so it can say it plainly, in the app's language. A node that does not answer
// is the code "node_down".
func withHint(err error) error {
	var e *client.Error
	switch {
	case errors.As(err, &e):
		b, _ := json.Marshal(map[string]string{"code": e.Code, "message": e.Message, "hint": e.Hint})
		return errors.New(string(b))
	case errors.Is(err, client.ErrUnavailable):
		b, _ := json.Marshal(map[string]string{"code": "node_down", "message": err.Error()})
		return errors.New(string(b))
	}
	return err
}

// Connection is the node as the app sees it.
type Connection struct {
	URL    string        `json:"url"`
	OK     bool          `json:"ok"`
	Error  string        `json:"error,omitempty"`
	Status client.Status `json:"status"`
}

// Status says whether the node answers, and what it is.
func (n *Node) Status() Connection {
	n.mu.Lock()
	failed := n.startErr != ""
	n.mu.Unlock()
	if failed || n.ownNodeEnded() { // the app's own node stopped after it got ready: started again
		if err := n.ensureNode(); err != nil {
			return Connection{Error: err.Error()}
		}
	}
	c, err := n.client()
	if err != nil {
		return Connection{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := c.Status(ctx)
	n.mu.Lock()
	url := n.url
	n.mu.Unlock()
	out := Connection{URL: url, OK: err == nil, Status: st}
	if err != nil {
		out.Error = err.Error()
		if errors.Is(err, client.ErrUnavailable) {
			out.Error = "the node is not running: start it with `acpost node` (or any acpost command)"
		}
	}
	return out
}

// Agents lists the agents: this node's, other nodes' through the hub, cloud sandboxes.
func (n *Node) Agents() ([]client.AgentInfo, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]client.AgentInfo, error) { return c.Agents(ctx) })
}

// Thread is a conversation's messages, the oldest first, with their bodies and replies: the
// whole thread, or its last threadMax messages. A message that cannot be read fails it.
func (n *Node) Thread(thread string) ([]store.Message, error) {
	return callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) ([]store.Message, error) {
		return threadOf(ctx, c, thread)
	})
}

// Upload is a file the composer attaches.
type Upload struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"` // base64
}

// Cancel cancels a message.
func (n *Node) Cancel(id string) (store.Message, error) {
	return call(n, func(ctx context.Context, c *client.Client) (store.Message, error) { return c.Cancel(ctx, id) })
}

// Answer answers a permission request: an option it offered, or "" to refuse.
func (n *Node) Answer(msgID, requestID, optionID string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		r := acpx.Reply{RequestID: requestID, OptionID: optionID}
		if optionID == "" {
			r.Action = acpx.ActionDecline
		}
		return struct{}{}, c.Answer(ctx, msgID, r)
	})
	return err
}

// Asking lists the permission requests waiting for a person.
func (n *Node) Asking() ([]client.AskingRequest, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]client.AskingRequest, error) { return c.Asking(ctx) })
}

// EventBatch is what "events:<id>" carries: the message's events after the last batch; Final the
// message once it is final (the stream ends there).
type EventBatch struct {
	ID     string         `json:"id"`
	Events []store.Event  `json:"events,omitempty"`
	Final  *store.Message `json:"final,omitempty"`
	Error  string         `json:"error,omitempty"`
	Gen    int64          `json:"gen,omitempty"` // the subscription it is of (Follow's gen)
}

// Follow streams a message's events after seq `after` as "events:<id>" until it is final (or
// Unfollow). Following it again restarts the stream. gen is this subscription's generation (the
// window's, rising): only the newest generation of a message changes its stream (followGen), so
// Follows and Unfollows that Wails runs out of order end as the window asked last.
func (n *Node) Follow(id string, after int64, gen int64) error {
	c, err := n.client()
	if err != nil {
		return withHint(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	me := &follow{cancel: cancel, gen: gen}
	n.mu.Lock()
	if !n.followGen(id, gen, false) {
		n.mu.Unlock()
		cancel()
		return nil // a newer subscription was asked, or this one was let go already: not started
	}
	if old := n.follows[id]; old != nil {
		old.cancel()
	}
	n.follows[id] = me
	n.mu.Unlock()
	go func() {
		// Only this stream's own entry goes: a Follow meanwhile put a newer one there.
		defer func() {
			n.mu.Lock()
			if n.follows[id] == me {
				delete(n.follows, id)
			}
			n.mu.Unlock()
		}()
		// Events come one at a time; they reach the interface a few times a second. One
		// goroutine emits at a time, the final after the last batch: the window drops a batch
		// whose seq is not after the last it applied.
		var mu sync.Mutex
		var pending []store.Event
		flush := func() {
			mu.Lock()
			evs := pending
			pending = nil
			mu.Unlock()
			if len(evs) > 0 {
				n.emitFollow(id, me, EventBatch{ID: id, Events: evs})
			}
		}
		tick := time.NewTicker(120 * time.Millisecond)
		defer tick.Stop()
		done := make(chan struct{})
		var ticking sync.WaitGroup
		ticking.Add(1)
		go func() {
			defer ticking.Done()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					flush()
				}
			}
		}()
		final, err := c.Follow(ctx, id, after, func(e store.Event) error {
			mu.Lock()
			pending = append(pending, e)
			mu.Unlock()
			return nil
		})
		close(done)
		ticking.Wait()
		flush()
		if ctx.Err() != nil {
			return
		}
		b := EventBatch{ID: id}
		if err != nil {
			b.Error = withHint(err).Error()
		} else {
			b.Final = &final
		}
		n.emitFollow(id, me, b)
	}()
	return nil
}

// Unfollow lets a message's subscription gen go, and every older one (0: whichever there is). Wails
// runs each call in its own goroutine: an Unfollow sent before a Follow may run after it, and
// must not stop the stream that Follow started; a Follow of gen that comes after it starts none.
func (n *Node) Unfollow(id string, gen int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if f := n.follows[id]; f != nil && n.followGen(id, gen, true) && (gen == 0 || f.gen <= gen) {
		f.cancel()
		delete(n.follows, id)
	}
}

// Events reads a message's events (no stream).
func (n *Node) Events(id string, after int64) ([]store.Event, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.Event, error) {
		return c.Events(ctx, id, after, 1000)
	})
}

// Changes is what a message's turns (and its subagents') changed, with the diffs.
func (n *Node) Changes(id string) (client.Changes, error) {
	return call(n, func(ctx context.Context, c *client.Client) (client.Changes, error) {
		return c.ChangesOf(ctx, id, true, true)
	})
}

// Usage sums usage since `days` days ago, grouped by agent, model, day or session.
func (n *Node) Usage(days int, by string) ([]client.UsageReport, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]client.UsageReport, error) {
		f := client.UsageFilter{By: by}
		if days > 0 {
			f.From = time.Now().AddDate(0, 0, -days)
		}
		return c.Usage(ctx, f)
	})
}

// Forge is an agent's repository's forge.
func (n *Node) Forge(agent string) (client.ForgeInfo, error) {
	return call(n, func(ctx context.Context, c *client.Client) (client.ForgeInfo, error) {
		return c.Forge(ctx, client.ForgeQuery{Agent: agent})
	})
}

// Pulls lists the pull requests of an agent's repository.
func (n *Node) Pulls(agent, state string) ([]forge.Pull, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]forge.Pull, error) {
		return c.Pulls(ctx, client.ForgeQuery{Agent: agent}, state)
	})
}

// Checks lists CI runs of a ref (the agent's branch when empty).
func (n *Node) Checks(agent, ref string) ([]forge.Check, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]forge.Check, error) {
		return c.Checks(ctx, client.ForgeQuery{Agent: agent}, ref)
	})
}

// CILog reads a CI job's log.
func (n *Node) CILog(agent, job, target string) (string, error) {
	return call(n, func(ctx context.Context, c *client.Client) (string, error) {
		return c.CILog(ctx, client.ForgeQuery{Agent: agent}, job, target)
	})
}

// OpenTerminal opens a shell (in the agent's working directory when one is named). Its output
// comes once the window attaches (TermAttach): the window subscribes to "term:<id>" first.
func (n *Node) OpenTerminal(agent string, cols, rows int) (client.Terminal, error) {
	// An open in flight is counted: shutdown waits for it, and a shell whose id comes after the
	// app began quitting is closed, not kept (on a node of another machine it would go on).
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return client.Terminal{}, errQuitting
	}
	n.opening.Add(1)
	n.mu.Unlock()
	defer n.opening.Done()
	t, err := callLong(n, termOpenTimeout, func(ctx context.Context, c *client.Client) (client.Terminal, error) {
		return c.OpenTerminal(ctx, client.TermRequest{Agent: agent, Cols: cols, Rows: rows})
	})
	if err != nil {
		return t, err
	}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		closeTerminals(n, []string{t.ID})
		return client.Terminal{}, errQuitting
	}
	if n.terms[t.ID] == nil {
		n.terms[t.ID] = &term{}
	}
	n.mu.Unlock()
	return t, nil
}

// TermAttach streams a terminal's output from offset from as "term:<id>": {data} (base64),
// {lost, error} while the connection is down (it comes back where it stopped), {exit, code}
// when the shell ends, {exit, error} when the node says the terminal is gone. Attaching again
// restarts the stream.
func (n *Node) TermAttach(id string, from int64) error {
	c, err := n.client()
	if err != nil {
		return withHint(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	me := &term{cancel: cancel}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		cancel()
		return errQuitting
	}
	if old := n.terms[id]; old != nil {
		old.stop()
	}
	n.terms[id] = me
	n.mu.Unlock()
	go func() {
		// The terminal stays the app's (CloseTerminal, shutdown) until its shell is known to have
		// ended: its end came, or the node says there is no such terminal.
		ended := false
		defer func() {
			n.mu.Lock()
			if ended && n.terms[id] == me {
				delete(n.terms, id)
			}
			n.mu.Unlock()
		}()
		lost, wait := false, time.Second
		for {
			final, err := c.TermOutput(ctx, id, from, func(next int64, b []byte) error {
				from, lost, wait = next, false, time.Second
				n.emitTerm(id, me, map[string]any{"data": base64.StdEncoding.EncodeToString(b)})
				return nil
			})
			if ctx.Err() != nil {
				return
			}
			switch termOutcome(err) {
			case termEnded:
				ended = true
				n.emitTerm(id, me, map[string]any{"exit": true, "code": final.ExitCode})
				return
			case termGone:
				ended = true
				n.emitTerm(id, me, map[string]any{"exit": true, "error": withHint(err).Error()})
				return
			case termRetry:
				if !lost {
					lost = true
					n.emitTerm(id, me, map[string]any{"lost": true, "error": withHint(err).Error()})
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
				wait = min(2*wait, 10*time.Second)
			default: // refused, invalid: said, and the terminal still closed with the app
				n.emitTerm(id, me, map[string]any{"exit": true, "error": withHint(err).Error()})
				return
			}
		}
	}()
	return nil
}

// TermInput types into a terminal (base64).
func (n *Node) TermInput(id, data string) error {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	_, err = call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.TermInput(ctx, id, b)
	})
	return err
}

// TermResize sizes a terminal.
func (n *Node) TermResize(id string, cols, rows int) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.TermResize(ctx, id, cols, rows)
	})
	return err
}

// CloseTerminal ends a terminal. Its output stops at once; its entry stays the app's until the
// node says the shell is closed (or that there is no such terminal): a close that did not reach
// the node is tried again, and shutdown still closes it. Its shell would go on, no one's.
func (n *Node) CloseTerminal(id string) error {
	n.mu.Lock()
	if t := n.terms[id]; t != nil {
		t.stop()
	}
	n.mu.Unlock()
	err := n.closeTerm(id)
	if err != nil {
		go n.retryClose(id, closeRetry, closeTries)
	}
	return err
}

// decodeUploads decodes the composer's files, the node's limits (when it says them) checked
// first on the encoded sizes: what is over them is never decoded.
func decodeUploads(files []Upload, lim *client.Limits) ([]store.File, error) {
	if lim != nil {
		if lim.MaxFiles > 0 && len(files) > lim.MaxFiles {
			return nil, &client.Error{Code: "invalid", Message: fmt.Sprintf("%d files: the node takes at most %d", len(files), lim.MaxFiles),
				Hint: "Send fewer files at once."}
		}
		var size int64
		for _, f := range files {
			size += int64(base64.StdEncoding.DecodedLen(len(f.Data)))
		}
		if lim.MaxFilesSize > 0 && size > lim.MaxFilesSize+2*int64(len(files)) { // DecodedLen counts the padding
			return nil, &client.Error{Code: "invalid", Message: fmt.Sprintf("the files come to %d MiB: the node takes at most %d MiB", size>>20, lim.MaxFilesSize>>20),
				Hint: "Attach smaller files, or give the agent a path on this Mac instead."}
		}
	}
	var out []store.File
	for _, f := range files {
		b, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		out = append(out, store.File{Name: f.Name, Type: f.Type, Size: len(b), Data: b})
	}
	return out, nil
}

// emit sends an event to the window (to emitFn in tests).
func (n *Node) emit(name string, data any) {
	if n.emitFn != nil {
		n.emitFn(name, data)
		return
	}
	n.app.Event.Emit(name, data)
}

// follow is a message's stream; its own goroutine removes it, when it is still the one there.
type follow struct {
	cancel context.CancelFunc
	gen    int64
}

// threadMax bounds what a thread shows: its latest messages.
const threadMax = 2000

func threadOf(ctx context.Context, c *client.Client, thread string) ([]store.Message, error) {
	// The list pages by id, the oldest first: read to the end, keep the end.
	var list []store.Message
	after := ""
	for {
		page, err := c.List(ctx, client.ListFilter{Thread: thread, After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		list = append(list, page.Items...)
		if len(list) > threadMax {
			list = list[len(list)-threadMax:]
		}
		if page.Next == "" || page.Next == after || len(page.Items) == 0 {
			break
		}
		after = page.Next
	}
	// The list has no bodies: each message read, a few at a time; the first failure ends it.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	out := make([]store.Message, len(list))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(8, len(list)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				m, err := c.Get(ctx, list[i].ID)
				if err != nil {
					cancel(fmt.Errorf("message %s: %w", list[i].ID, err))
					continue
				}
				out[i] = m
			}
		}()
	}
feed:
	for i := range list {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		var e *client.Error
		if errors.As(err, &e) {
			return nil, &client.Error{Status: e.Status, Code: e.Code, Message: err.Error(), Hint: e.Hint}
		}
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// term is a terminal the app opened; cancel stops its output stream, once attached.
type term struct{ cancel context.CancelFunc }

func (t *term) stop() {
	if t.cancel != nil {
		t.cancel()
	}
}

// closeTerminals ends terminals on the node, all at once, for at most two seconds.
func closeTerminals(n *Node, ids []string) {
	c, err := n.client()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.CloseTerminal(ctx, id) }()
	}
	wg.Wait()
}

// emitFollow emits a stream's batch only while the stream is the one registered for its
// message: a batch of a stream replaced meanwhile would land in the new one's turn.
func (n *Node) emitFollow(id string, me *follow, b EventBatch) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.follows[id] == me {
		b.Gen = me.gen
		n.emit("events:"+id, b)
	}
}

var errQuitting = &client.Error{Code: "unavailable", Message: "the app is quitting"}

const (
	termEnded  = iota // the shell's end came
	termGone          // the node has no such terminal
	termRetry         // the connection, or the node for now: read again from where it stopped
	termFailed        // refused, invalid: not to be retried
)

// termOutcome is what a terminal stream's end means.
func termOutcome(err error) int {
	if err == nil {
		return termEnded
	}
	if errors.Is(err, client.ErrUnavailable) {
		return termRetry
	}
	var e *client.Error
	if errors.As(err, &e) {
		switch {
		case e.Code == "not_found" || e.Status == http.StatusNotFound:
			return termGone
		case e.Status >= 500 || e.Status == http.StatusTooManyRequests || e.Code == "unavailable" || e.Code == "timeout":
			return termRetry
		}
	}
	return termFailed
}

// emitTerm emits a terminal stream's event only while the stream is the one attached: a chunk
// of a stream replaced meanwhile would interleave with the new one's (terminal output has no
// seq to drop it by).
func (n *Node) emitTerm(id string, me *term, ev map[string]any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.terms[id] == me {
		n.emit("term:"+id, ev)
	}
}

// termOpenTimeout bounds an open of a terminal; shutdown waits for opens in flight that long.
const termOpenTimeout = 30 * time.Second

// closeTerm asks the node to end a terminal; once it is known ended (closed, or the node has no
// such terminal), the terminal is no longer the app's.
func (n *Node) closeTerm(id string) error {
	c, err := n.client()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = c.CloseTerminal(ctx, id)
		cancel()
	}
	if err != nil && termOutcome(err) != termGone {
		return withHint(err)
	}
	n.mu.Lock()
	if t := n.terms[id]; t != nil {
		t.stop()
		delete(n.terms, id)
	}
	n.mu.Unlock()
	return nil
}

// closeRetry is the first wait before a failed close of a terminal is tried again (doubling,
// closeTries times); what is still not closed then is closed with the app.
var closeRetry, closeTries = time.Second, 8

func (n *Node) retryClose(id string, wait time.Duration, tries int) {
	for range tries {
		time.Sleep(wait)
		wait = min(2*wait, 30*time.Second)
		n.mu.Lock()
		_, mine := n.terms[id]
		quitting := n.closed
		n.mu.Unlock()
		if !mine || quitting { // closed meanwhile, or shutdown closes it
			return
		}
		if n.closeTerm(id) == nil {
			return
		}
	}
}

// followGens is what the window asked last of a message's subscriptions: the newest generation
// followed, and the newest let go (it and those before it start nothing).
type followGens struct{ newest, gone int64 }

// followGen says whether a Follow (unfollow false) or an Unfollow of generation gen is the
// newest word on message id's stream, and records it. Generation 0 (tests, an older window)
// always is. Called with n.mu held.
func (n *Node) followGen(id string, gen int64, unfollow bool) bool {
	if gen == 0 {
		return true
	}
	if n.gens == nil {
		n.gens = map[string]*followGens{}
	}
	g := n.gens[id]
	if g == nil {
		g = &followGens{}
		n.gens[id] = g
	}
	if unfollow {
		g.gone = max(g.gone, gen)
		return true
	}
	if gen <= g.gone || gen < g.newest {
		return false
	}
	g.newest = gen
	return true
}

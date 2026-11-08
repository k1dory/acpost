//go:build darwin

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
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
	terms    map[string]context.CancelFunc
	stopNode context.CancelFunc // the node the app runs itself, if it does
	nodeDone chan struct{}      // closed when that node has stopped
	startMu  sync.Mutex         // one ensureNode at a time
	notes    *notifications.NotificationService
	noteOK   int // 0 not asked, 1 allowed, -1 refused
	startErr string
	emitFn   func(name string, data any) // tests: where events go instead of the window
}

// ServiceStartup connects to the node in the background: the window opens meanwhile (a node
// that is slow to start held it for up to 20 s), and Status waits for the outcome.
func (n *Node) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	n.follows = map[string]*follow{}
	n.terms = map[string]context.CancelFunc{}
	n.startErr = "starting" // Status goes through ensureNode, which waits for this start
	go n.ensureNode()
	return nil
}

// ServiceShutdown stops the streams.
func (n *Node) ServiceShutdown() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, f := range n.follows {
		f.cancel()
	}
	for _, stop := range n.terms {
		stop()
	}
	if n.stopNode != nil {
		n.stopNode()
	}
	// The node cleans up (its socket, its pid file) before the app exits.
	if done := n.nodeDone; done != nil {
		n.mu.Unlock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		n.mu.Lock()
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
	var zero T
	c, err := n.client()
	if err != nil {
		return zero, withHint(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := fn(ctx, c)
	return out, withHint(err)
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
	if failed {
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

// ConversationPage is a page of conversations.
type ConversationPage struct {
	Items []store.Conversation `json:"items"`
	Next  string               `json:"next,omitempty"`
}

// Conversations lists threads, the most recently active first.
func (n *Node) Conversations(before string) (ConversationPage, error) {
	return call(n, func(ctx context.Context, c *client.Client) (ConversationPage, error) {
		items, next, err := c.Conversations(ctx, before, 50)
		return ConversationPage{Items: items, Next: next}, err
	})
}

// Thread is a conversation's messages, the oldest first, with their bodies and replies: the
// whole thread, or its last threadMax messages. A message that cannot be read fails it.
func (n *Node) Thread(thread string) ([]store.Message, error) {
	return callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) ([]store.Message, error) {
		return threadOf(ctx, c, thread)
	})
}

// Message is one message as it is now.
func (n *Node) Message(id string) (store.Message, error) {
	return call(n, func(ctx context.Context, c *client.Client) (store.Message, error) { return c.Get(ctx, id) })
}

// Upload is a file the composer attaches.
type Upload struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"` // base64
}

// Send sends a message: to an agent, in a thread (a new one when empty).
func (n *Node) Send(to, body, thread string, files []Upload) (store.Message, error) {
	return call(n, func(ctx context.Context, c *client.Client) (store.Message, error) {
		r := client.SendRequest{To: to, Body: body, Thread: thread}
		fs, err := decodeUploads(files)
		if err != nil {
			return store.Message{}, err
		}
		r.Files = fs
		m, _, err := c.Send(ctx, r, "")
		return m, err
	})
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
}

// Follow streams a message's events after seq `after` as "events:<id>" until it is final (or
// Unfollow). Following it again restarts the stream.
func (n *Node) Follow(id string, after int64) error {
	c, err := n.client()
	if err != nil {
		return withHint(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	me := &follow{cancel: cancel}
	n.mu.Lock()
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
			if len(evs) > 0 && ctx.Err() == nil {
				n.emit("events:"+id, EventBatch{ID: id, Events: evs})
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
		n.emit("events:"+id, b)
	}()
	return nil
}

// Unfollow stops a message's stream.
func (n *Node) Unfollow(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if f := n.follows[id]; f != nil {
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

// OpenTerminal opens a shell (in the agent's working directory when one is named) and streams
// its output as "term:<id>" (base64 chunks).
func (n *Node) OpenTerminal(agent string, cols, rows int) (client.Terminal, error) {
	t, err := call(n, func(ctx context.Context, c *client.Client) (client.Terminal, error) {
		return c.OpenTerminal(ctx, client.TermRequest{Agent: agent, Cols: cols, Rows: rows})
	})
	if err != nil {
		return t, err
	}
	c, _ := n.client()
	ctx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	n.terms[t.ID] = cancel
	n.mu.Unlock()
	go func() {
		final, err := c.TermOutput(ctx, t.ID, 0, func(_ int64, b []byte) error {
			n.emit("term:"+t.ID, map[string]any{"data": base64.StdEncoding.EncodeToString(b)})
			return nil
		})
		if ctx.Err() == nil {
			ev := map[string]any{"exit": true, "code": final.ExitCode}
			if err != nil {
				ev["error"] = err.Error()
			}
			n.emit("term:"+t.ID, ev)
		}
	}()
	return t, nil
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

// CloseTerminal ends a terminal.
func (n *Node) CloseTerminal(id string) error {
	n.mu.Lock()
	if stop := n.terms[id]; stop != nil {
		stop()
		delete(n.terms, id)
	}
	n.mu.Unlock()
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.CloseTerminal(ctx, id)
	})
	return err
}

func decodeUploads(files []Upload) ([]store.File, error) {
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
type follow struct{ cancel context.CancelFunc }

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

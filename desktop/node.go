//go:build darwin

package main

import (
	"context"
	"encoding/base64"
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
)

// Node is the app's link to the local acpost node: what the interface calls (bindings) and the
// streams it follows (events "events:<message id>", "term:<terminal id>").
type Node struct {
	app *application.App

	mu      sync.Mutex
	c       *client.Client
	url     string
	follows  map[string]context.CancelFunc
	terms    map[string]context.CancelFunc
	stopNode context.CancelFunc // the node the app runs itself, if it does
	startErr string
}

// ServiceStartup connects to the node (a failure is told by Status, the app still opens).
func (n *Node) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	n.follows = map[string]context.CancelFunc{}
	n.terms = map[string]context.CancelFunc{}
	if err := n.ensureNode(); err != nil {
		n.startErr = err.Error()
	}
	return nil
}

// ServiceShutdown stops the streams.
func (n *Node) ServiceShutdown() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, stop := range n.follows {
		stop()
	}
	for _, stop := range n.terms {
		stop()
	}
	if n.stopNode != nil {
		n.stopNode()
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
		return zero, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return fn(ctx, c)
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
	c, err := n.client()
	if err != nil {
		return Connection{Error: err.Error()}
	}
	if n.startErr != "" {
		if err := n.ensureNode(); err != nil {
			return Connection{Error: err.Error()}
		}
		n.startErr = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := c.Status(ctx)
	out := Connection{URL: n.url, OK: err == nil, Status: st}
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

// Thread is a conversation's messages, the oldest first, with their bodies and replies.
func (n *Node) Thread(thread string) ([]store.Message, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.Message, error) {
		page, err := c.List(ctx, client.ListFilter{Thread: thread, Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]store.Message, 0, len(page.Items))
		for _, m := range page.Items {
			full, err := c.Get(ctx, m.ID)
			if err != nil {
				full = m
			}
			out = append(out, full)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
		return out, nil
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
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	if stop := n.follows[id]; stop != nil {
		stop()
	}
	n.follows[id] = cancel
	n.mu.Unlock()
	go func() {
		defer func() {
			n.mu.Lock()
			if n.follows[id] != nil {
				delete(n.follows, id)
			}
			n.mu.Unlock()
		}()
		// Events come one at a time; they reach the interface a few times a second.
		var mu sync.Mutex
		var pending []store.Event
		flush := func() {
			mu.Lock()
			evs := pending
			pending = nil
			mu.Unlock()
			if len(evs) > 0 {
				n.app.Event.Emit("events:"+id, EventBatch{ID: id, Events: evs})
			}
		}
		tick := time.NewTicker(120 * time.Millisecond)
		defer tick.Stop()
		done := make(chan struct{})
		go func() {
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
		flush()
		if ctx.Err() != nil {
			return
		}
		b := EventBatch{ID: id}
		if err != nil {
			b.Error = err.Error()
		} else {
			b.Final = &final
		}
		n.app.Event.Emit("events:"+id, b)
	}()
	return nil
}

// Unfollow stops a message's stream.
func (n *Node) Unfollow(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if stop := n.follows[id]; stop != nil {
		stop()
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
			n.app.Event.Emit("term:"+t.ID, map[string]any{"data": base64.StdEncoding.EncodeToString(b)})
			return nil
		})
		if ctx.Err() == nil {
			ev := map[string]any{"exit": true, "code": final.ExitCode}
			if err != nil {
				ev["error"] = err.Error()
			}
			n.app.Event.Emit("term:"+t.ID, ev)
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

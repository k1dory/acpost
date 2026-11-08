//go:build darwin

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/server"
	"github.com/k1dory/acpost/internal/store"
)

// The app's own node: when none answers on the configured socket, the app runs one itself (the
// configuration written first if there is none: Claude Code and Codex over ACP, the cloud of the
// first agent-sandbox env file found).

// runNode runs the app's own node (a stub in tests).
var runNode = server.Run

// nodeStartWait is how long the app's own node has to get ready.
var nodeStartWait = 20 * time.Second

// ensureNode answers whether a node serves now, starting one in the app when none does. Calls
// are serialized (Wails runs each binding call in its own goroutine, and Status is called at
// start, on a timer and from "Try again"): one node starts, the others find it running. The
// outcome is kept for Status.
func (n *Node) ensureNode() error {
	n.startMu.Lock()
	defer n.startMu.Unlock()
	err := n.startNode()
	n.mu.Lock()
	n.startErr = ""
	if err != nil {
		n.startErr = err.Error()
	}
	n.mu.Unlock()
	return err
}

func (n *Node) startNode() error {
	if n.ownNodeRunning() {
		return nil
	}
	c, err := n.client()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err = c.Status(ctx)
		cancel()
		if err == nil {
			return nil
		}
	}
	if os.Getenv("ACPOST_NODE") != "" {
		return fmt.Errorf("the node at ACPOST_NODE does not answer: %w", err)
	}
	path := config.DefaultPath()
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := writeDefaultConfig(path); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("%s: %w (fix it, or remove it for the app to write one)", path, err)
	}
	logPath := filepath.Join(config.StateDir(), "app-node.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	ready := make(chan struct{})
	var readyOnce sync.Once
	failed := make(chan error, 1)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	n.stopNode, n.nodeDone = cancel, done
	n.mu.Unlock()
	go func() {
		defer close(done)
		defer logf.Close()
		log := slog.New(slog.NewTextHandler(logf, nil))
		if err := runNode(ctx, cfg, log, func() { readyOnce.Do(func() { close(ready) }) }); err != nil {
			failed <- err
		}
	}()
	select {
	case <-ready:
		n.mu.Lock()
		n.c = nil // reconnect to the node now listening
		n.mu.Unlock()
		return nil
	case err := <-failed:
		cancel()
		return fmt.Errorf("the node did not start: %w (log %s)", err, logPath)
	case <-done:
		return fmt.Errorf("the node stopped as it started (log %s)", logPath)
	case <-time.After(nodeStartWait):
		// Not left starting: the next try would start another beside it.
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return fmt.Errorf("the node did not start in %s (log %s)", nodeStartWait, logPath)
	}
}

// ownNodeRunning says whether the node the app started still runs.
func (n *Node) ownNodeRunning() bool {
	n.mu.Lock()
	done := n.nodeDone
	n.mu.Unlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// writeDefaultConfig writes a node for this Mac: the ACP agents found, and a cloud when an
// agent-sandbox env file is there.
func writeDefaultConfig(path string) error {
	var b strings.Builder
	b.WriteString("# Written by the acpost app. See the README for what else goes here.\nagents:\n")
	found := 0
	for _, a := range []struct{ name, bin string }{{"claude", "claude-agent-acp"}, {"codex", "codex-acp"}} {
		p, err := exec.LookPath(a.bin)
		if err != nil {
			for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
				if _, e := os.Stat(filepath.Join(dir, a.bin)); e == nil {
					p, err = filepath.Join(dir, a.bin), nil
					break
				}
			}
		}
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "  %s:\n    driver: acp\n    command: [%q]\n    permissions: ask\n    session: thread\n    concurrency: 4\n", a.name, p)
		found++
	}
	if found == 0 {
		return fmt.Errorf("neither claude-agent-acp nor codex-acp is installed (npm i -g @agentclientprotocol/claude-agent-acp @agentclientprotocol/codex-acp)")
	}
	home, _ := os.UserHomeDir()
	if envs, _ := filepath.Glob(filepath.Join(home, ".config", "agent-sandbox", "*.env")); len(envs) > 0 {
		fmt.Fprintf(&b, "cloud: {env_file: %q}\n", envs[0])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Written whole, and only where there is none: a configuration that appeared meanwhile is kept.
	if err := writeAtomic(path, []byte(b.String()), 0o600, false); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// Chats lists the user's chats (threads, Claude Code and Codex sessions, cloud sandboxes) and folders.
func (n *Node) Chats() (client.Chats, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) (client.Chats, error) { return c.Chats(ctx) })
}

// Options is what an agent of this Mac offers to choose (models, effort, modes) and its commands.
func (n *Node) Options(agent string) (client.AgentOptions, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) (client.AgentOptions, error) {
		return c.Options(ctx, strings.TrimPrefix(agent, "agent:"))
	})
}

// SessionHistory replays a Claude Code or Codex session started anywhere.
func (n *Node) SessionHistory(agent, id, cwd string) ([]store.Event, error) {
	return callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) ([]store.Event, error) {
		return c.SessionHistory(ctx, strings.TrimPrefix(agent, "agent:"), id, cwd)
	})
}

// CloudAgents lists the cloud tenant's catalogue.
// CloudCatalog is the cloud's catalogue, and whether there is a cloud at all.
type CloudCatalog struct {
	Items      []client.CloudAgent `json:"items"`
	Configured bool                `json:"configured"`
}

func (n *Node) CloudAgents() (CloudCatalog, error) {
	return call(n, func(ctx context.Context, c *client.Client) (CloudCatalog, error) {
		items, configured, err := c.CloudCatalog(ctx)
		return CloudCatalog{Items: items, Configured: configured}, err
	})
}

// CloudHistory reads a cloud sandbox's conversation.
func (n *Node) CloudHistory(id string) ([]store.Event, error) {
	return callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) ([]store.Event, error) { return c.CloudHistory(ctx, id) })
}

// CreateCloudTask makes the sandbox of a new cloud task.
func (n *Node) CreateCloudTask(agent, repo, name string) (client.Chat, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) (client.Chat, error) {
		return c.CreateCloudTask(ctx, client.CloudTask{Agent: agent, Repo: repo, Name: name})
	})
}

// Task is a message as the composer sends it.
type Task struct {
	To      string   `json:"to"`
	Body    string   `json:"body"`
	Thread  string   `json:"thread,omitempty"`
	Session string   `json:"session,omitempty"` // continue an ACP session started elsewhere
	Cwd     string   `json:"cwd,omitempty"`
	Model   string   `json:"model,omitempty"`
	Effort  string   `json:"effort,omitempty"`
	Mode    string   `json:"mode,omitempty"`
	Files   []Upload `json:"files,omitempty"`
	// Idem is the send's idempotency key, the composer's for this one message: a retry after a
	// lost answer gets the message already made instead of a second run.
	Idem string `json:"idem,omitempty"`
}

// SendTask sends a message with its directory and settings.
func (n *Node) SendTask(t Task) (store.Message, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) (store.Message, error) {
		r := client.SendRequest{To: t.To, Body: t.Body, Thread: t.Thread, Session: t.Session, Cwd: t.Cwd, Model: t.Model, Effort: t.Effort, Mode: t.Mode}
		var lim *client.Limits
		if len(t.Files) > 0 {
			st, err := c.Status(ctx)
			if err != nil {
				return store.Message{}, err
			}
			lim = st.Limits
		}
		files, err := decodeUploads(t.Files, lim)
		if err != nil {
			return store.Message{}, err
		}
		r.Files = files
		m, _, err := c.Send(ctx, r, t.Idem)
		return m, err
	})
}

// CreateFolder, RenameFolder, DeleteFolder, MoveChat: the app's folders of chats.
func (n *Node) CreateFolder(name string) (store.Folder, error) {
	return call(n, func(ctx context.Context, c *client.Client) (store.Folder, error) { return c.CreateFolder(ctx, name) })
}

func (n *Node) RenameFolder(id, name string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.UpdateFolder(ctx, id, name, nil)
	})
	return err
}

func (n *Node) DeleteFolder(id string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.DeleteFolder(ctx, id)
	})
	return err
}

func (n *Node) MoveChat(chat, folder string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.MoveChat(ctx, chat, folder)
	})
	return err
}

// PickDirectory asks for a directory (the system's dialog); "" when none was chosen.
func (n *Node) PickDirectory(start string) (string, error) {
	d := n.app.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).SetTitle("The task's directory")
	if start != "" {
		d = d.SetDirectory(start)
	}
	return d.PromptForSingleSelection()
}

// Home is the user's home directory (the default task directory).
func (n *Node) Home() string {
	h, _ := os.UserHomeDir()
	return h
}

// callLong calls the node with a deadline of d; its errors come whole (withHint), the
// client's own too.
func callLong[T any](n *Node, d time.Duration, fn func(ctx context.Context, c *client.Client) (T, error)) (T, error) {
	var zero T
	c, err := n.client()
	if err != nil {
		return zero, withHint(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	out, err := fn(ctx, c)
	return out, withHint(err)
}

// ForkSession forks a chat's agent session: the new session, a chat of its own.
func (n *Node) ForkSession(agent, id, cwd string) (string, error) {
	return callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) (string, error) {
		return c.ForkSession(ctx, strings.TrimPrefix(agent, "agent:"), id, cwd)
	})
}

// EditQueued changes what a queued message asks.
func (n *Node) EditQueued(id, body string) (string, error) {
	m, err := call(n, func(ctx context.Context, c *client.Client) (store.Message, error) { return c.EditQueued(ctx, id, body) })
	return m.Body, err
}

// Handoff sends a local chat on to the cloud: the cloud chat it goes on in.
func (n *Node) Handoff(r client.HandoffRequest) (client.HandoffResult, error) {
	return callLong(n, 3*time.Minute, func(ctx context.Context, c *client.Client) (client.HandoffResult, error) {
		return c.Handoff(ctx, r)
	})
}

// Tree is what agents asked other agents under a message.
func (n *Node) Tree(id string) ([]client.TreeNode, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]client.TreeNode, error) { return c.Tree(ctx, id) })
}

// FileAt is a file as a turn left it.
func (n *Node) FileAt(repo, tree, path string) (string, error) {
	return call(n, func(ctx context.Context, c *client.Client) (string, error) { return c.FileAt(ctx, repo, tree, path) })
}

func (n *Node) Backlog() ([]store.BacklogItem, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.BacklogItem, error) { return c.Backlog(ctx) })
}

func (n *Node) PutBacklogItem(b store.BacklogItem) (store.BacklogItem, error) {
	return call(n, func(ctx context.Context, c *client.Client) (store.BacklogItem, error) {
		return c.PutBacklogItem(ctx, b)
	})
}

func (n *Node) DeleteBacklogItem(id string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.DeleteBacklogItem(ctx, id)
	})
	return err
}

// StartBacklogItem gives a backlog item to an agent: the thread it starts.
func (n *Node) StartBacklogItem(id, agent string) (string, error) {
	m, err := call(n, func(ctx context.Context, c *client.Client) (store.Message, error) {
		return c.StartBacklogItem(ctx, id, agent)
	})
	return cmp.Or(m.Thread, m.ID), err
}

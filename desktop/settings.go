//go:build darwin

package main

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// Settings: forge accounts, MCP servers; and the system's notifications.

func (n *Node) ForgeAccounts() ([]client.ForgeAccount, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) ([]client.ForgeAccount, error) {
		return c.ForgeAccounts(ctx)
	})
}

func (n *Node) ForgeLogin(host, kind, token string) (client.ForgeAccount, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) (client.ForgeAccount, error) {
		return c.ForgeLogin(ctx, host, kind, token)
	})
}

func (n *Node) ForgeLogout(host string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.ForgeLogout(ctx, host)
	})
	return err
}

func (n *Node) MCPServers() ([]store.MCPServer, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.MCPServer, error) { return c.MCPServers(ctx) })
}

// PutMCPServer adds or changes an MCP server; secrets: "env/NAME" or "header/NAME" → value
// (absent: kept as is; empty: forgotten).
func (n *Node) PutMCPServer(m store.MCPServer, secrets map[string]string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.PutMCPServer(ctx, client.MCPServer{MCPServer: m, Secrets: secrets})
	})
	return err
}

func (n *Node) DeleteMCPServer(name string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.DeleteMCPServer(ctx, name)
	})
	return err
}

// Notify shows a notification of the system (asked for once; nothing when refused or unavailable).
func (n *Node) Notify(title, body string) error {
	if n.notes == nil {
		return nil
	}
	// Asked once, however many replies finish at once; the answer kept for the rest.
	var askErr error
	n.noteAsk.Do(func() {
		ok, err := n.notes.RequestNotificationAuthorization()
		n.mu.Lock()
		n.noteOK = -1
		if ok && err == nil {
			n.noteOK = 1
		}
		n.mu.Unlock()
		askErr = err
	})
	n.mu.Lock()
	ok := n.noteOK > 0
	n.mu.Unlock()
	if !ok {
		return askErr
	}
	return n.notes.SendNotification(notifications.NotificationOptions{
		ID: fmt.Sprintf("acpost-%d", time.Now().UnixNano()), Title: title, Body: body})
}

// notifier is what Notify uses of the system's notifications.
type notifier interface {
	RequestNotificationAuthorization() (bool, error)
	SendNotification(notifications.NotificationOptions) error
}

func (n *Node) Playbooks() ([]store.Playbook, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.Playbook, error) { return c.Playbooks(ctx) })
}

func (n *Node) PutPlaybook(name, body string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.PutPlaybook(ctx, name, body)
	})
	return err
}

func (n *Node) DeletePlaybook(name string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.DeletePlaybook(ctx, name)
	})
	return err
}

func (n *Node) SetChatTitle(chat, title string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.SetChatTitle(ctx, chat, title)
	})
	return err
}

func (n *Node) Schedules() ([]store.Schedule, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.Schedule, error) { return c.Schedules(ctx) })
}

func (n *Node) PutSchedule(sc store.Schedule) (store.Schedule, error) {
	return call(n, func(ctx context.Context, c *client.Client) (store.Schedule, error) { return c.PutSchedule(ctx, sc) })
}

func (n *Node) DeleteSchedule(id string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.DeleteSchedule(ctx, id)
	})
	return err
}

func (n *Node) RunSchedule(id string) (string, error) {
	m, err := call(n, func(ctx context.Context, c *client.Client) (store.Message, error) { return c.RunSchedule(ctx, id) })
	return m.ID, err
}

func (n *Node) Knowledge() ([]store.Note, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.Note, error) { return c.Knowledge(ctx) })
}

func (n *Node) PutNote(k store.Note) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) { return struct{}{}, c.PutNote(ctx, k) })
	return err
}

func (n *Node) DeleteNote(name string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.DeleteNote(ctx, name)
	})
	return err
}

func (n *Node) Wikis() ([]store.Wiki, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.Wiki, error) { return c.Wikis(ctx) })
}

func (n *Node) WikiPages(repo string) ([]store.WikiPage, error) {
	return call(n, func(ctx context.Context, c *client.Client) ([]store.WikiPage, error) { return c.WikiPages(ctx, repo) })
}

// WriteWiki starts writing a directory's wiki: the task's thread.
func (n *Node) WriteWiki(repo, agent, lang string) (string, error) {
	m, err := callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) (store.Message, error) {
		return c.WriteWiki(ctx, repo, agent, lang)
	})
	return cmp.Or(m.Thread, m.ID), err
}

func (n *Node) DeleteWiki(repo string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.DeleteWiki(ctx, repo)
	})
	return err
}

// Package client talks to a node's HTTP API: the command line, a joined node and any Go program use it.
package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/k1dory/acpost/internal/forge"
	"github.com/k1dory/acpost/internal/store"
	"github.com/k1dory/acpx"
)

// SendRequest is the body of POST /v1/messages.
type SendRequest struct {
	To     string `json:"to"`
	Body   string `json:"body"`
	Task   string `json:"task,omitempty"`
	Thread string `json:"thread,omitempty"`
	// Session continues an existing ACP session of the agent.
	Session string `json:"session,omitempty"`
	// Cwd is the directory a new task's session runs in (an ACP agent of the node); Model, Effort
	// and Mode are selected in the session before this message's turn.
	Cwd    string            `json:"cwd,omitempty"`
	Model  string            `json:"model,omitempty"`
	Effort string            `json:"effort,omitempty"`
	Mode   string            `json:"mode,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
	Files  store.Files       `json:"files,omitempty"`
	TTL    string            `json:"ttl,omitempty"`
	// Set only by a node forwarding its own message to a hub.
	ID     string `json:"id,omitempty"`
	From   string `json:"from,omitempty"`
	Parent string `json:"parent,omitempty"`
	Depth  int    `json:"depth,omitempty"`
}

// ReplyRequest is the body of POST /v1/messages/{id}/reply.
// RelayRequest carries events a joined node's agent recorded for a message the node holds under
// lease, to the hub's copy of it: those after the node's event number After.
type RelayRequest struct {
	Lease  string               `json:"lease"`
	After  int64                `json:"after"`
	Events []store.RelayedEvent `json:"events"`
}

type ReplyRequest struct {
	Lease string `json:"lease"`
	Body  string `json:"body,omitempty"`
	Error string `json:"error,omitempty"`
	// ErrorCode is the failure's code (README, "Errors"); a joined node passes on its agent's.
	ErrorCode string      `json:"error_code,omitempty"`
	Files     store.Files `json:"files,omitempty"`
}

// AttachRequest is the body of POST /v1/attach: files an agent adds to the reply it works on.
type AttachRequest struct {
	Files store.Files `json:"files"`
}

// Page is a page of a list.
type Page struct {
	Items []store.Message `json:"items"`
	Next  string          `json:"next,omitempty"`
}

// AgentInfo is an agent as GET /v1/agents shows it.
type AgentInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Driver  string `json:"driver"` // acp, inbox, remote
	Node    string `json:"node,omitempty"`
	Queued  int    `json:"queued"`
	Running int    `json:"running"`
	Process bool   `json:"process,omitempty"` // an acp agent's process is up
}

// AgentOptions is what an ACP agent offers to choose (models, reasoning levels, modes) and its
// slash commands.
type AgentOptions struct {
	Agent    string         `json:"agent"`
	Options  []AgentOption  `json:"options"`
	Commands []AgentCommand `json:"commands,omitempty"`
}

// AgentOption is one choice: its category (model, thought_level, mode), the current value, the values.
type AgentOption struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Category string        `json:"category,omitempty"`
	Current  string        `json:"current"`
	Values   []OptionValue `json:"values"`
}

type OptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group,omitempty"`
}

type AgentCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Options is what an ACP agent offers to choose.
func (c *Client) Options(ctx context.Context, agent string) (AgentOptions, error) {
	var out AgentOptions
	_, err := c.do(ctx, "GET", "/v1/agents/"+url.PathEscape(agent)+"/options", nil, nil, &out, nil)
	return out, err
}

// SessionHistory replays an agent's ACP session: its turns as events (user, text, thought, tool calls…).
func (c *Client) SessionHistory(ctx context.Context, agent, id, cwd string) ([]store.Event, error) {
	q := url.Values{}
	if cwd != "" {
		q.Set("cwd", cwd)
	}
	var out struct {
		Items []store.Event `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/agents/"+url.PathEscape(agent)+"/sessions/"+url.PathEscape(id)+"/history", q, nil, &out, nil)
	return out.Items, err
}

// Chat is one of the node's user's chats: an acpost thread, an ACP session of an agent of the
// node (started anywhere), or a cloud sandbox.
type Chat struct {
	Key     string `json:"key"`  // thread:<id>, session:<agent>/<id>, cloud:<sandbox>
	Kind    string `json:"kind"` // thread, session, cloud
	Agent   string `json:"agent,omitempty"`
	Title   string `json:"title"`
	Cwd     string `json:"cwd,omitempty"`
	Repo    string `json:"repo,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Session string `json:"session,omitempty"` // the ACP session (a thread's, or the chat itself)
	// SessionAgent is the agent whose session Session is (a thread's agents differ).
	SessionAgent string    `json:"session_agent,omitempty"`
	Sandbox      string    `json:"sandbox,omitempty"`
	Status       string    `json:"status,omitempty"` // a sandbox's
	Updated      time.Time `json:"updated"`
	Messages     int       `json:"messages,omitempty"`
	Running      int       `json:"running,omitempty"`
	Queued       int       `json:"queued,omitempty"`
	Failed       int       `json:"failed,omitempty"`
	Folder       string    `json:"folder,omitempty"`
	// CloudAgent is the catalogue agent a sandbox was made from.
	CloudAgent string `json:"cloud_agent,omitempty"`
}

// CloudAgent is an agent of the cloud tenant's catalogue.
type CloudAgent struct {
	Ref         string `json:"ref"` // as a sandbox is made from it: name, or shared/name
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Revision    int    `json:"revision"`
}

// CloudTask starts a cloud task: a sandbox of a catalogue agent, on a repository if one is named.
type CloudTask struct {
	Agent string `json:"agent"`
	Repo  string `json:"repo,omitempty"`
	Name  string `json:"name,omitempty"`
}

// CloudAgents lists the cloud tenant's catalogue.
func (c *Client) CloudAgents(ctx context.Context) ([]CloudAgent, error) {
	var out struct {
		Items []CloudAgent `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/cloud/agents", nil, nil, &out, nil)
	return out.Items, err
}

// CloudCatalog is the catalogue and whether a cloud is configured at all (an error, from a node
// that has one, is that cloud failing).
func (c *Client) CloudCatalog(ctx context.Context) ([]CloudAgent, bool, error) {
	var out struct {
		Items      []CloudAgent `json:"items"`
		Configured *bool        `json:"configured"` // absent from an older node: a catalogue means a cloud
	}
	_, err := c.do(ctx, "GET", "/v1/cloud/agents", nil, nil, &out, nil)
	configured := len(out.Items) > 0
	if out.Configured != nil {
		configured = *out.Configured
	}
	return out.Items, configured, err
}

// CloudHistory reads a cloud sandbox's conversation as events.
func (c *Client) CloudHistory(ctx context.Context, id string) ([]store.Event, error) {
	var out struct {
		Items []store.Event `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/cloud/sandboxes/"+url.PathEscape(id)+"/history", nil, nil, &out, nil)
	return out.Items, err
}

// CreateCloudTask makes the sandbox of a new cloud task; its messages go to agent:<sandbox id>.
func (c *Client) CreateCloudTask(ctx context.Context, t CloudTask) (Chat, error) {
	var out Chat
	_, err := c.do(ctx, "POST", "/v1/cloud/tasks", nil, t, &out, nil)
	return out, err
}

// EditQueued changes the body of a queued message.
func (c *Client) EditQueued(ctx context.Context, id, body string) (store.Message, error) {
	var out store.Message
	_, err := c.do(ctx, "PATCH", "/v1/messages/"+url.PathEscape(id), nil, map[string]string{"body": body}, &out, nil)
	return out, err
}

// ForkSession forks an agent's session (into cwd, or the session's own directory): the new one.
func (c *Client) ForkSession(ctx context.Context, agent, id, cwd string) (string, error) {
	var out struct {
		Session string `json:"session"`
	}
	_, err := c.do(ctx, "POST", "/v1/agents/"+url.PathEscape(agent)+"/sessions/"+url.PathEscape(id)+"/fork", nil,
		map[string]string{"cwd": cwd}, &out, nil)
	return out.Session, err
}

// Chats is the list, the folders, and what could not be listed (an agent that did not answer).
type Chats struct {
	Items   []Chat         `json:"items"`
	Folders []store.Folder `json:"folders"`
	Partial []string       `json:"partial,omitempty"`
}

// Chats lists the node's user's chats, the most recently active first, with the folders.
func (c *Client) Chats(ctx context.Context) (Chats, error) {
	var out Chats
	_, err := c.do(ctx, "GET", "/v1/chats", nil, nil, &out, nil)
	return out, err
}

// ForgeAccount is a forge host logged in to.
type ForgeAccount struct {
	Host    string `json:"host"`
	Kind    string `json:"kind,omitempty"`
	Version string `json:"version,omitempty"`
	Authed  bool   `json:"authed"`
	Login   string `json:"login,omitempty"` // whose the token is
	Error   string `json:"error,omitempty"`
}

// ForgeAccounts lists the forge hosts logged in to.
func (c *Client) ForgeAccounts(ctx context.Context) ([]ForgeAccount, error) {
	var out struct {
		Items []ForgeAccount `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/forge/accounts", nil, nil, &out, nil)
	return out.Items, err
}

// ForgeLogin keeps a host's token (checked against the forge first).
func (c *Client) ForgeLogin(ctx context.Context, host, kind, token string) (ForgeAccount, error) {
	var out ForgeAccount
	_, err := c.do(ctx, "POST", "/v1/forge/accounts", nil, map[string]string{"host": host, "kind": kind, "token": token}, &out, nil)
	return out, err
}

// ForgeLogout forgets a host's token.
func (c *Client) ForgeLogout(ctx context.Context, host string) error {
	_, err := c.do(ctx, "DELETE", "/v1/forge/accounts/"+url.PathEscape(host), nil, nil, nil, nil)
	return err
}

// MCPServer is an MCP server the agents get, with secret values to keep (env/NAME, header/NAME).
type MCPServer struct {
	store.MCPServer
	Secrets map[string]string `json:"secrets,omitempty"`
}

// MCPServers lists the MCP servers the agents get.
func (c *Client) MCPServers(ctx context.Context) ([]store.MCPServer, error) {
	var out struct {
		Items []store.MCPServer `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/mcp", nil, nil, &out, nil)
	return out.Items, err
}

// PutMCPServer adds or changes an MCP server.
func (c *Client) PutMCPServer(ctx context.Context, m MCPServer) error {
	_, err := c.do(ctx, "PUT", "/v1/mcp/"+url.PathEscape(m.Name), nil, m, nil, nil)
	return err
}

// DeleteMCPServer removes an MCP server and its secrets.
func (c *Client) DeleteMCPServer(ctx context.Context, name string) error {
	_, err := c.do(ctx, "DELETE", "/v1/mcp/"+url.PathEscape(name), nil, nil, nil, nil)
	return err
}

// Backlog lists the tasks kept for later.
func (c *Client) Backlog(ctx context.Context) ([]store.BacklogItem, error) {
	var out struct {
		Items []store.BacklogItem `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/backlog", nil, nil, &out, nil)
	return out.Items, err
}

// PutBacklogItem adds (no id) or changes a backlog item.
func (c *Client) PutBacklogItem(ctx context.Context, b store.BacklogItem) (store.BacklogItem, error) {
	var out store.BacklogItem
	_, err := c.do(ctx, "PUT", "/v1/backlog", nil, b, &out, nil)
	return out, err
}

// DeleteBacklogItem removes one.
func (c *Client) DeleteBacklogItem(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DELETE", "/v1/backlog/"+url.PathEscape(id), nil, nil, nil, nil)
	return err
}

// StartBacklogItem gives one to its agent (or agent): the message.
func (c *Client) StartBacklogItem(ctx context.Context, id, agent string) (store.Message, error) {
	var out store.Message
	_, err := c.do(ctx, "POST", "/v1/backlog/"+url.PathEscape(id)+"/start", nil, map[string]string{"agent": agent}, &out, nil)
	return out, err
}

// TreeNode is a message an agent sent while working on another.
type TreeNode struct {
	ID     string      `json:"id"`
	Parent string      `json:"parent"`
	Depth  int         `json:"depth"`
	From   string      `json:"from"`
	To     string      `json:"to"`
	Thread string      `json:"thread"`
	State  store.State `json:"state"`
	Body   string      `json:"body"`
	Reply  string      `json:"reply"`
	At     time.Time   `json:"at"`
}

// Tree is what was asked under a message (subagents), in order.
func (c *Client) Tree(ctx context.Context, id string) ([]TreeNode, error) {
	var out struct {
		Items []TreeNode `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/messages/"+url.PathEscape(id)+"/tree", nil, nil, &out, nil)
	return out.Items, err
}

// FileAt is a file as a turn left it.
func (c *Client) FileAt(ctx context.Context, repo, tree, path string) (string, error) {
	var out struct {
		Text string `json:"text"`
	}
	_, err := c.do(ctx, "GET", "/v1/file-at", url.Values{"repo": {repo}, "tree": {tree}, "path": {path}}, nil, &out, nil)
	return out.Text, err
}

// HandoffRequest is a local chat to go on in the cloud.
type HandoffRequest struct {
	Thread     string `json:"thread,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Session    string `json:"session,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	CloudAgent string `json:"cloud_agent"`
	Note       string `json:"note,omitempty"`
}

// HandoffResult is the cloud chat it goes on in, and what went with it.
type HandoffResult struct {
	Chat    Chat     `json:"chat"`
	Message string   `json:"message"`
	Repo    string   `json:"repo"`
	Branch  string   `json:"branch,omitempty"`
	Base    string   `json:"base,omitempty"`
	Patch   int      `json:"patch"`
	Left    []string `json:"left_out,omitempty"`
}

// Handoff sends a local chat on to the cloud.
func (c *Client) Handoff(ctx context.Context, r HandoffRequest) (HandoffResult, error) {
	var out HandoffResult
	_, err := c.do(ctx, "POST", "/v1/handoff", nil, r, &out, nil)
	return out, err
}

// Wikis lists the repositories' wikis.
func (c *Client) Wikis(ctx context.Context) ([]store.Wiki, error) {
	var out struct {
		Items []store.Wiki `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/wiki", nil, nil, &out, nil)
	return out.Items, err
}

// WikiPages is a wiki's pages with their text.
func (c *Client) WikiPages(ctx context.Context, repo string) ([]store.WikiPage, error) {
	var out struct {
		Items []store.WikiPage `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/wiki/pages", url.Values{"repo": {repo}}, nil, &out, nil)
	return out.Items, err
}

// WriteWiki has an agent write a directory's wiki: the task doing it.
func (c *Client) WriteWiki(ctx context.Context, repo, agent, lang string) (store.Message, error) {
	var out store.Message
	_, err := c.do(ctx, "POST", "/v1/wiki", nil, map[string]string{"repo": repo, "agent": agent, "lang": lang}, &out, nil)
	return out, err
}

// DeleteWiki forgets a wiki.
func (c *Client) DeleteWiki(ctx context.Context, repo string) error {
	_, err := c.do(ctx, "DELETE", "/v1/wiki", url.Values{"repo": {repo}}, nil, nil, nil)
	return err
}

// Knowledge lists the notes agents are given at a new session.
func (c *Client) Knowledge(ctx context.Context) ([]store.Note, error) {
	var out struct {
		Items []store.Note `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/knowledge", nil, nil, &out, nil)
	return out.Items, err
}

// PutNote adds or changes a note.
func (c *Client) PutNote(ctx context.Context, k store.Note) error {
	_, err := c.do(ctx, "PUT", "/v1/knowledge", nil, k, nil, nil)
	return err
}

// DeleteNote removes a note.
func (c *Client) DeleteNote(ctx context.Context, name string) error {
	_, err := c.do(ctx, "DELETE", "/v1/knowledge/"+url.PathEscape(name), nil, nil, nil, nil)
	return err
}

// Schedules lists the scheduled tasks.
func (c *Client) Schedules(ctx context.Context) ([]store.Schedule, error) {
	var out struct {
		Items []store.Schedule `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/schedules", nil, nil, &out, nil)
	return out.Items, err
}

// PutSchedule adds (no id) or changes a scheduled task.
func (c *Client) PutSchedule(ctx context.Context, sc store.Schedule) (store.Schedule, error) {
	var out store.Schedule
	_, err := c.do(ctx, "PUT", "/v1/schedules", nil, sc, &out, nil)
	return out, err
}

// DeleteSchedule removes a scheduled task.
func (c *Client) DeleteSchedule(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DELETE", "/v1/schedules/"+url.PathEscape(id), nil, nil, nil, nil)
	return err
}

// RunSchedule sends a scheduled task now.
func (c *Client) RunSchedule(ctx context.Context, id string) (store.Message, error) {
	var out store.Message
	_, err := c.do(ctx, "POST", "/v1/schedules/"+url.PathEscape(id)+"/run", nil, nil, &out, nil)
	return out, err
}

// Playbooks lists the kept prompts.
func (c *Client) Playbooks(ctx context.Context) ([]store.Playbook, error) {
	var out struct {
		Items []store.Playbook `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/playbooks", nil, nil, &out, nil)
	return out.Items, err
}

// PutPlaybook keeps a prompt under a name.
func (c *Client) PutPlaybook(ctx context.Context, name, body string) error {
	_, err := c.do(ctx, "PUT", "/v1/playbooks/"+url.PathEscape(name), nil, map[string]string{"body": body}, nil, nil)
	return err
}

// DeletePlaybook forgets a prompt.
func (c *Client) DeletePlaybook(ctx context.Context, name string) error {
	_, err := c.do(ctx, "DELETE", "/v1/playbooks/"+url.PathEscape(name), nil, nil, nil, nil)
	return err
}

// SetChatTitle names a chat ("" gives it back its own title).
func (c *Client) SetChatTitle(ctx context.Context, chat, title string) error {
	_, err := c.do(ctx, "PUT", "/v1/chats/title", nil, map[string]string{"chat": chat, "title": title}, nil, nil)
	return err
}

// Folders lists the app's folders of chats, with their chats.
func (c *Client) Folders(ctx context.Context) ([]store.Folder, error) {
	var out struct {
		Items []store.Folder `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/folders", nil, nil, &out, nil)
	return out.Items, err
}

// CreateFolder makes a folder.
func (c *Client) CreateFolder(ctx context.Context, name string) (store.Folder, error) {
	var out store.Folder
	_, err := c.do(ctx, "POST", "/v1/folders", nil, map[string]string{"name": name}, &out, nil)
	return out, err
}

// UpdateFolder renames a folder ("" keeps the name) and moves it to pos (nil keeps it).
func (c *Client) UpdateFolder(ctx context.Context, id, name string, pos *int) error {
	body := map[string]any{"name": name}
	if pos != nil {
		body["pos"] = *pos
	}
	_, err := c.do(ctx, "PATCH", "/v1/folders/"+url.PathEscape(id), nil, body, nil, nil)
	return err
}

// DeleteFolder removes a folder.
func (c *Client) DeleteFolder(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DELETE", "/v1/folders/"+url.PathEscape(id), nil, nil, nil, nil)
	return err
}

// MoveChat puts a chat in a folder ("" takes it out).
func (c *Client) MoveChat(ctx context.Context, chat, folder string) error {
	_, err := c.do(ctx, "PUT", "/v1/chats/folder", nil, map[string]string{"chat": chat, "folder": folder}, nil, nil)
	return err
}

// SessionInfo is one of an ACP agent's sessions.
type SessionInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Cwd       string `json:"cwd"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Sessions lists an acp agent's sessions, newest first; next continues the list.
func (c *Client) Sessions(ctx context.Context, agent, cwd, cursor string) ([]SessionInfo, string, error) {
	var out struct {
		Items []SessionInfo `json:"items"`
		Next  string        `json:"next,omitempty"`
	}
	q := url.Values{}
	if cwd != "" {
		q.Set("cwd", cwd)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	_, err := c.do(ctx, "GET", "/v1/agents/"+url.PathEscape(agent)+"/sessions", q, nil, &out, nil)
	return out.Items, out.Next, err
}

// Status is GET /v1/health.
type Status struct {
	OK      bool   `json:"ok"`
	Node    string `json:"node"`
	Version string `json:"version"`
	You     string `json:"you"`
	Hub     string `json:"hub,omitempty"`
	// Limits a sender checks before reading what it sends.
	Limits *Limits `json:"limits,omitempty"`
}

// Limits are the node's limits on what a message carries.
type Limits struct {
	MaxBody      int64 `json:"max_body"`
	MaxFiles     int   `json:"max_files"`
	MaxFilesSize int64 `json:"max_files_size"`
}

// Announce is the body of POST /v1/nodes/{name}/announce.
type Announce struct {
	Agents []string `json:"agents"`
}

// Error is an error the node returned.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// ErrUnavailable wraps failures to reach the node at all.
var ErrUnavailable = errors.New("node unavailable")

// Client is a connection to one node.
type Client struct {
	base  string
	token string
	hc    *http.Client
	URL   string
	// MaxResponse bounds the bytes of a response read (0: no bound): a node reading its hub.
	MaxResponse int64
}

// New makes a client for a node URL: unix:///path/to.sock, http://host:port or https://host:port.
// caFile, when set, is the CA bundle a https node's certificate is checked against.
func New(nodeURL, token, caFile string) (*Client, error) {
	c := &Client{token: token, URL: nodeURL}
	// Reaching the node is bounded: a host that drops the connect or stalls the TLS handshake is
	// unavailable within seconds. The answer is not: long polls hold it up to MaxWait, and an
	// agent's forge request waits for a person's approval (ask_timeout); callers bound those.
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	tr := &http.Transport{MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: handshakeTimeout,
		DialContext: d.DialContext}
	switch {
	case strings.HasPrefix(nodeURL, "unix://"):
		sock := strings.TrimPrefix(nodeURL, "unix://")
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", sock)
		}
		c.base = "http://acpost"
	case strings.HasPrefix(nodeURL, "http://"), strings.HasPrefix(nodeURL, "https://"):
		c.base = strings.TrimRight(nodeURL, "/")
		if caFile != "" {
			pem, err := os.ReadFile(caFile)
			if err != nil {
				return nil, err
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("%s: no certificates", caFile)
			}
			tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		}
	default:
		return nil, fmt.Errorf("node %q: unix:///path, http://host:port or https://host:port", nodeURL)
	}
	c.hc = &http.Client{Transport: tr}
	return c, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any, header http.Header) (int, error) {
	var body io.Reader
	if in != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // < > & as they are: a body's size on the wire stays close to its own
		if err := enc.Encode(in); err != nil {
			return 0, err
		}
		body = &buf
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("%w: %s: %w", ErrUnavailable, c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return resp.StatusCode, respErr(method, path, resp)
	}
	if out == nil {
		// Read what is left of a small answer, so the connection goes back to the pool; not
		// for long: a body that does not come is cut, and the call has its answer already.
		cut := time.AfterFunc(drainTimeout, func() { resp.Body.Close() })
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		cut.Stop()
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		cr := &capReader{r: resp.Body, left: c.MaxResponse}
		if err := json.NewDecoder(cr).Decode(out); err != nil {
			// Only what is wrong with the content is final; anything the connection did may pass.
			var se *json.SyntaxError
			var te *json.UnmarshalTypeError
			switch {
			case cr.hit:
				return resp.StatusCode, fmt.Errorf("%s %s: %w: over %d bytes", method, path, ErrBadResponse, c.MaxResponse)
			case errors.As(err, &se), errors.As(err, &te), errors.Is(err, store.ErrBadFiles):
				return resp.StatusCode, fmt.Errorf("%s %s: %w: %v", method, path, ErrBadResponse, err)
			}
			// The connection broke while the response came: it may work the next time.
			return resp.StatusCode, fmt.Errorf("%w: %s: %s %s: %w", ErrUnavailable, c.URL, method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// Send sends a message. created is false when an earlier message with the same idempotency key
// was returned instead.
func (c *Client) Send(ctx context.Context, r SendRequest, idemKey string) (store.Message, bool, error) {
	var m store.Message
	var h http.Header
	if idemKey != "" {
		h = http.Header{"Idempotency-Key": {idemKey}}
	}
	code, err := c.do(ctx, "POST", "/v1/messages", nil, r, &m, h)
	return m, code == http.StatusCreated, err
}

func (c *Client) Get(ctx context.Context, id string) (store.Message, error) {
	var m store.Message
	_, err := c.do(ctx, "GET", "/v1/messages/"+url.PathEscape(id), nil, nil, &m, nil)
	return m, err
}

// MaxWait is the longest one wait request lasts; the server caps it the same way.
const MaxWait = 60 * time.Second

// Wait waits up to timeout (at most MaxWait) for a message to be final and returns it as it is then.
func (c *Client) Wait(ctx context.Context, id string, timeout time.Duration) (store.Message, error) {
	var m store.Message
	q := url.Values{"timeout": {min(timeout, MaxWait).String()}}
	_, err := c.do(ctx, "GET", "/v1/messages/"+url.PathEscape(id)+"/wait", q, nil, &m, nil)
	return m, err
}

// WaitFinal waits until the message is final or timeout passes (timeout 0: no limit), long-polling.
func (c *Client) WaitFinal(ctx context.Context, id string, timeout time.Duration) (store.Message, error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		step := MaxWait
		if !deadline.IsZero() {
			step = min(step, time.Until(deadline))
		}
		if step < 0 {
			step = 0
		}
		// The request itself is bounded: a node that stalls cannot hold the caller past its timeout.
		var rctx context.Context
		var done context.CancelFunc
		if deadline.IsZero() {
			rctx, done = context.WithTimeout(ctx, step+5*time.Second)
		} else {
			rctx, done = context.WithDeadline(ctx, deadline.Add(50*time.Millisecond))
		}
		m, err := c.Wait(rctx, id, step)
		done()
		if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			if !deadline.IsZero() && !time.Now().Before(deadline) {
				return m, &Error{Code: "timeout", Message: "timed out waiting for " + id + "; the message keeps going"}
			}
			continue // a stalled long poll: ask again
		}
		if err != nil || m.State.Final() || (!deadline.IsZero() && !time.Now().Before(deadline)) {
			return m, err
		}
	}
}

// ListFilter is the query of GET /v1/messages.
type ListFilter struct {
	To, From, Task, Thread, State, After string
	Limit                                int
}

func (c *Client) List(ctx context.Context, f ListFilter) (Page, error) {
	q := url.Values{}
	for k, v := range map[string]string{"to": f.To, "from": f.From, "task": f.Task, "thread": f.Thread, "state": f.State, "after": f.After} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	var p Page
	_, err := c.do(ctx, "GET", "/v1/messages", q, nil, &p, nil)
	return p, err
}

func (c *Client) Events(ctx context.Context, id string, after int64, limit int) ([]store.Event, error) {
	var out struct {
		Items []store.Event `json:"items"`
	}
	q := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	_, err := c.do(ctx, "GET", "/v1/messages/"+url.PathEscape(id)+"/events", q, nil, &out, nil)
	return out.Items, err
}

func (c *Client) Cancel(ctx context.Context, id string) (store.Message, error) {
	var m store.Message
	_, err := c.do(ctx, "POST", "/v1/messages/"+url.PathEscape(id)+"/cancel", nil, nil, &m, nil)
	return m, err
}

// Claim takes a message for an inbox agent; ok is false when none came within wait.
func (c *Client) Claim(ctx context.Context, agent string, wait, lease time.Duration) (store.Message, bool, error) {
	var m store.Message
	q := url.Values{"wait": {min(wait, MaxWait).String()}, "lease": {lease.String()}}
	code, err := c.do(ctx, "POST", "/v1/agents/"+url.PathEscape(agent)+"/claim", q, nil, &m, nil)
	return m, err == nil && code == http.StatusOK, err
}

func (c *Client) Reply(ctx context.Context, id string, r ReplyRequest) error {
	_, err := c.do(ctx, "POST", "/v1/messages/"+url.PathEscape(id)+"/reply", nil, r, nil, nil)
	return err
}

// capReader reads at most left bytes (0: no bound) and records reaching the bound.
type capReader struct {
	r    io.Reader
	left int64
	hit  bool
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left == 0 && c.hit {
		return 0, io.ErrUnexpectedEOF
	}
	if c.left > 0 {
		if int64(len(p)) > c.left {
			p = p[:c.left]
		}
		n, err := c.r.Read(p)
		c.left -= int64(n)
		if c.left == 0 {
			c.hit = true
		}
		return n, err
	}
	return c.r.Read(p)
}

// ErrBadResponse is a response that could not be read: malformed, or over MaxResponse.
var ErrBadResponse = errors.New("bad response")

// Files returns a message's files with their data.
func (c *Client) Files(ctx context.Context, id string) ([]store.File, error) {
	var out struct {
		Items store.Files `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/messages/"+url.PathEscape(id)+"/files", nil, nil, &out, nil)
	return out.Items, err
}

// SideFiles returns the files of one side of a message (the reply's, or the message's own), with
// their data, reading at most max bytes of response (0: no bound).
func (c *Client) SideFiles(ctx context.Context, id string, reply bool, max int64) ([]store.File, error) {
	cc := *c
	cc.MaxResponse = max
	side := "message"
	if reply {
		side = "reply"
	}
	var out struct {
		Items store.Files `json:"items"`
	}
	_, err := cc.do(ctx, "GET", "/v1/messages/"+url.PathEscape(id)+"/files", url.Values{"side": {side}}, nil, &out, nil)
	return out.Items, err
}

// Attach adds files to the reply the calling agent process works on; it returns that message's id.
func (c *Client) Attach(ctx context.Context, files []store.File) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	_, err := c.do(ctx, "POST", "/v1/attach", nil, AttachRequest{Files: files}, &out, nil)
	return out.ID, err
}

func (c *Client) Extend(ctx context.Context, id, lease string, d time.Duration) error {
	_, err := c.do(ctx, "POST", "/v1/messages/"+url.PathEscape(id)+"/lease", url.Values{"lease": {lease}, "for": {d.String()}}, nil, nil, nil)
	return err
}

func (c *Client) Agents(ctx context.Context) ([]AgentInfo, error) {
	var out []AgentInfo
	_, err := c.do(ctx, "GET", "/v1/agents", nil, nil, &out, nil)
	return out, err
}

// Status is cheap for a node that works: one that does not answer it within statusTimeout is
// unavailable (unless ctx has a deadline of its own).
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	parent := ctx
	if _, ok := ctx.Deadline(); !ok {
		var done context.CancelFunc
		ctx, done = context.WithTimeout(ctx, statusTimeout)
		defer done()
	}
	_, err := c.do(ctx, "GET", "/v1/health", nil, nil, &s, nil)
	if errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil { // our bound, not the caller's
		return s, fmt.Errorf("%w: %s: no answer to a health check within %v", ErrUnavailable, c.URL, statusTimeout)
	}
	return s, err
}

// Announce tells a hub which agents this node serves.
func (c *Client) Announce(ctx context.Context, node string, agents []string) error {
	_, err := c.do(ctx, "POST", "/v1/nodes/"+url.PathEscape(node)+"/announce", nil, Announce{Agents: agents}, nil, nil)
	return err
}

// NodeClaim takes a message a hub holds for one of this node's agents.
func (c *Client) NodeClaim(ctx context.Context, node string, wait, lease time.Duration) (store.Message, bool, error) {
	var m store.Message
	q := url.Values{"wait": {min(wait, MaxWait).String()}, "lease": {lease.String()}}
	code, err := c.do(ctx, "POST", "/v1/nodes/"+url.PathEscape(node)+"/claim", q, nil, &m, nil)
	return m, err == nil && code == http.StatusOK, err
}

// respErr is the API error a response of status 400 or more carries.
func respErr(method, path string, resp *http.Response) error {
	e := respError(method, path, resp)
	if e.Code == "unavailable" {
		// The node says what a refused connection says: not now, maybe the next time or node.
		return fmt.Errorf("%w: %w", ErrUnavailable, e)
	}
	return e
}

// respError is the API error a response of status 400 or more carries.
func respError(method, path string, resp *http.Response) *Error {
	var eb struct {
		Error Error `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if json.Unmarshal(b, &eb) != nil || eb.Error.Code == "" {
		eb.Error = Error{Code: "internal", Message: fmt.Sprintf("%s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(b)))}
	}
	eb.Error.Status = resp.StatusCode
	return &eb.Error
}

// Follow streams a message's events as they are recorded (Server-Sent Events) and calls fn with
// each, until the message is final; it returns the final message. after skips the events up to
// that seq. A broken stream is reopened where it stopped (Last-Event-ID); an API error or fn's
// error ends it.
func (c *Client) Follow(ctx context.Context, id string, after int64, fn func(store.Event) error) (store.Message, error) {
	pause := followRetry
	for {
		seen := after
		m, done, err := c.followOnce(ctx, id, &after, fn)
		if done || err != nil && (ctx.Err() != nil || !errors.Is(err, ErrUnavailable)) {
			return m, err
		}
		// The connection dropped: again, from the last event seen; sooner when it was moving, ever
		// later (to a ceiling) while a node that is down stays down.
		if after > seen {
			pause = followRetry
		}
		select {
		case <-ctx.Done():
			return m, ctx.Err()
		case <-time.After(pause):
		}
		pause = min(2*pause, followRetryMax)
	}
}

// followOnce reads one SSE connection; done says the end came.
func (c *Client) followOnce(ctx context.Context, id string, after *int64, fn func(store.Event) error) (store.Message, bool, error) {
	path := "/v1/messages/" + url.PathEscape(id) + "/events"
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path+"?follow=1&after="+strconv.FormatInt(*after, 10), nil)
	if err != nil {
		return store.Message{}, false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return store.Message{}, false, ctx.Err()
		}
		return store.Message{}, false, fmt.Errorf("%w: %s: %w", ErrUnavailable, c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return store.Message{}, false, respErr("GET", path, resp)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		// A node of an older version answers with a page of events: no stream to follow there.
		return store.Message{}, false, fmt.Errorf("%w: %s does not stream events", ErrBadResponse, c.URL)
	}
	rd := bufio.NewReaderSize(resp.Body, 64<<10)
	var kind string
	var data strings.Builder
	for {
		line, err := readLine(rd, maxEventLine)
		if errors.Is(err, errLongLine) {
			return store.Message{}, false, fmt.Errorf("%w: %v", ErrBadResponse, err) // the same again on a retry
		}
		if err != nil {
			if ctx.Err() != nil {
				return store.Message{}, false, ctx.Err()
			}
			return store.Message{}, false, fmt.Errorf("%w: %s: the event stream broke: %w", ErrUnavailable, c.URL, err)
		}
		switch {
		case line == "": // the end of one event
			if data.Len() == 0 {
				kind = ""
				continue
			}
			if kind == streamErrorEvent {
				return store.Message{}, false, streamErr(data.String())
			}
			if kind == "end" {
				var m store.Message
				if err := json.Unmarshal([]byte(data.String()), &m); err != nil {
					return m, false, fmt.Errorf("%w: the end of the event stream: %w", ErrBadResponse, err)
				}
				return m, true, nil
			}
			var e store.Event
			if err := json.Unmarshal([]byte(data.String()), &e); err != nil {
				return store.Message{}, false, fmt.Errorf("%w: an event: %w", ErrBadResponse, err)
			}
			if e.Seq > *after {
				if err := fn(e); err != nil {
					return store.Message{}, false, err
				}
				*after = e.Seq
			}
			kind = ""
			data.Reset()
		case strings.HasPrefix(line, ":"): // a comment: the stream is alive
		case strings.HasPrefix(line, "event:"):
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

// maxEventLine bounds one line of an event stream: the end carries the whole message (a body and
// a reply of the largest sizes a node may take, escaped).
var maxEventLine = 64 << 20

var errLongLine = errors.New("an event stream line is over its limit")

// readLine reads one line without its end, of at most max bytes.
func readLine(rd *bufio.Reader, max int) (string, error) {
	var b []byte
	for {
		part, more, err := rd.ReadLine()
		if err != nil {
			return "", err
		}
		b = append(b, part...)
		if len(b) > max {
			return "", fmt.Errorf("%w (%d bytes)", errLongLine, max)
		}
		if !more {
			return string(b), nil
		}
	}
}

// RelayEvents hands the hub the events of a message this node holds under lease, those after its
// event number after; it returns how many of this node's events the hub has taken.
func (c *Client) RelayEvents(ctx context.Context, id, lease string, after int64, evs []store.RelayedEvent) (int64, error) {
	var out struct {
		Relayed int64 `json:"relayed"`
	}
	_, err := c.do(ctx, "POST", "/v1/messages/"+url.PathEscape(id)+"/events", nil, RelayRequest{Lease: lease, After: after, Events: evs}, &out, nil)
	return out.Relayed, err
}

// UsageReport is a group of turns' usage, with what the node adds: the cache's share of what was
// read, and an estimate (by the node's prices) for turns whose agent gave no cost.
type UsageReport struct {
	store.UsageSum
	CacheRatio        float64  `json:"cache_ratio"`
	Estimated         float64  `json:"estimated,omitempty"`
	EstimatedTurns    int      `json:"estimated_turns,omitempty"`
	EstimatedCurrency string   `json:"estimated_currency,omitempty"`
	Unpriced          []string `json:"unpriced,omitempty"` // models of uncosted turns the node has no price for
	UnpricedTurns     int      `json:"unpriced_turns,omitempty"`
}

// UsageFilter selects the usage to sum: [From, To), grouped By agent, model, day or session.
type UsageFilter struct {
	From, To time.Time
	By       string
	Agent    string
}

// Usage sums agents' usage as f says.
func (c *Client) Usage(ctx context.Context, f UsageFilter) ([]UsageReport, error) {
	q := url.Values{}
	if !f.From.IsZero() {
		q.Set("from", f.From.Format(time.RFC3339))
	}
	if !f.To.IsZero() {
		q.Set("to", f.To.Format(time.RFC3339))
	}
	if f.By != "" {
		q.Set("by", f.By)
	}
	if f.Agent != "" {
		q.Set("agent", f.Agent)
	}
	var out struct {
		Items []UsageReport `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/usage", q, nil, &out, nil)
	return out.Items, err
}

// Changes is what a message's turns changed (and its subagents' with tree): each turn's files and
// diff, and each repository's diff over them all.
type Changes struct {
	Turns []store.TurnChanges `json:"turns"`
	Repos []store.RepoDiff    `json:"repos,omitempty"`
}

// ChangesOf is what a message's turns changed; tree adds its subagents', diff the diffs.
func (c *Client) ChangesOf(ctx context.Context, id string, tree, diff bool) (Changes, error) {
	q := url.Values{}
	if tree {
		q.Set("tree", "1")
	}
	if diff {
		q.Set("diff", "1")
	}
	var out Changes
	_, err := c.do(ctx, "GET", "/v1/messages/"+url.PathEscape(id)+"/changes", q, nil, &out, nil)
	return out, err
}

// Terminal is a terminal on a node (node.Terminal on the wire).
type Terminal struct {
	ID       string    `json:"id"`
	Cwd      string    `json:"cwd"`
	Agent    string    `json:"agent,omitempty"`
	Started  time.Time `json:"started"`
	Running  bool      `json:"running"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Size     int64     `json:"size"`
}

// TermRequest opens a terminal: in an agent's working directory, or Cwd.
type TermRequest struct {
	Agent string `json:"agent,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

func (c *Client) OpenTerminal(ctx context.Context, r TermRequest) (Terminal, error) {
	var t Terminal
	_, err := c.do(ctx, "POST", "/v1/terminals", nil, r, &t, nil)
	return t, err
}

func (c *Client) Terminals(ctx context.Context) ([]Terminal, error) {
	var out struct {
		Items []Terminal `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/terminals", nil, nil, &out, nil)
	return out.Items, err
}

// TermInput types b into a terminal.
func (c *Client) TermInput(ctx context.Context, id string, b []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/v1/terminals/"+url.PathEscape(id)+"/input", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.unreachable(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return respErr("POST", "/v1/terminals/"+id+"/input", resp)
	}
	return nil
}

func (c *Client) TermResize(ctx context.Context, id string, cols, rows int) error {
	_, err := c.do(ctx, "POST", "/v1/terminals/"+url.PathEscape(id)+"/resize", nil, map[string]int{"cols": cols, "rows": rows}, nil, nil)
	return err
}

func (c *Client) CloseTerminal(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DELETE", "/v1/terminals/"+url.PathEscape(id), nil, nil, nil, nil)
	return err
}

// TermOutput streams a terminal's output from offset from, calling fn with each piece and the
// offset after it (where to come back from), until its shell ends (it returns the terminal then)
// or ctx ends.
func (c *Client) TermOutput(ctx context.Context, id string, from int64, fn func(next int64, b []byte) error) (Terminal, error) {
	path := "/v1/terminals/" + url.PathEscape(id) + "/output"
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path+"?from="+strconv.FormatInt(from, 10), nil)
	if err != nil {
		return Terminal{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Terminal{}, c.unreachable(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Terminal{}, respErr("GET", path, resp)
	}
	rd := bufio.NewReaderSize(resp.Body, 64<<10)
	var kind, data string
	var next int64
	for {
		line, err := readLine(rd, 1<<20)
		if err != nil {
			if ctx.Err() != nil {
				return Terminal{}, ctx.Err()
			}
			return Terminal{}, fmt.Errorf("%w: %s: the terminal's stream broke: %w", ErrUnavailable, c.URL, err)
		}
		switch {
		case line == "":
			switch kind {
			case "data":
				b, err := base64.StdEncoding.DecodeString(data)
				if err != nil {
					return Terminal{}, fmt.Errorf("%w: terminal output: %w", ErrBadResponse, err)
				}
				if err := fn(next, b); err != nil {
					return Terminal{}, err
				}
			case "end":
				var t Terminal
				if err := json.Unmarshal([]byte(data), &t); err != nil {
					return t, fmt.Errorf("%w: terminal end: %w", ErrBadResponse, err)
				}
				return t, nil
			case streamErrorEvent:
				return Terminal{}, streamErr(data)
			}
			kind, data = "", ""
		case strings.HasPrefix(line, "event:"):
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "id:"):
			next, _ = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64)
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		}
	}
}

// ForgeQuery names a repository: an agent's working directory, or a directory of the node's.
type ForgeQuery struct {
	Agent string `json:"agent,omitempty"`
	Dir   string `json:"dir,omitempty"`
}

func (q ForgeQuery) values() url.Values {
	v := url.Values{}
	if q.Agent != "" {
		v.Set("agent", q.Agent)
	}
	if q.Dir != "" {
		v.Set("dir", q.Dir)
	}
	return v
}

// ForgeInfo is a repository's forge as the node sees it.
type ForgeInfo struct {
	Repo   forge.Repo   `json:"repo"`
	Server forge.Server `json:"server"`
	Branch string       `json:"branch,omitempty"`
	Authed bool         `json:"authed"`
}

func (c *Client) Forge(ctx context.Context, q ForgeQuery) (ForgeInfo, error) {
	var out ForgeInfo
	_, err := c.do(ctx, "GET", "/v1/forge", q.values(), nil, &out, nil)
	return out, err
}

func (c *Client) Pulls(ctx context.Context, q ForgeQuery, state string) ([]forge.Pull, error) {
	v := q.values()
	if state != "" {
		v.Set("state", state)
	}
	var out struct {
		Items []forge.Pull `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/forge/pulls", v, nil, &out, nil)
	return out.Items, err
}

func (c *Client) Checks(ctx context.Context, q ForgeQuery, ref string) ([]forge.Check, error) {
	v := q.values()
	if ref != "" {
		v.Set("ref", ref)
	}
	var out struct {
		Items []forge.Check `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/forge/checks", v, nil, &out, nil)
	return out.Items, err
}

// CILog is a CI job's log, its secrets masked.
func (c *Client) CILog(ctx context.Context, q ForgeQuery, job, target string) (string, error) {
	v := q.values()
	v.Set("job", job)
	if target != "" {
		v.Set("target", target)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/forge/log?"+v.Encode(), nil)
	if err != nil {
		return "", err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", c.unreachable(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", respErr("GET", "/v1/forge/log", resp)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(b), err
}

func (c *Client) OpenPull(ctx context.Context, q ForgeQuery, p forge.PullRequest) (forge.Pull, error) {
	body := struct {
		ForgeQuery
		forge.PullRequest
	}{q, p}
	var out forge.Pull
	_, err := c.do(ctx, "POST", "/v1/forge/pulls", nil, body, &out, nil)
	return out, err
}

// MergePull merges a pull request (an agent's waits for a person's approval).
func (c *Client) MergePull(ctx context.Context, q ForgeQuery, m forge.Merge) error {
	body := struct {
		ForgeQuery
		forge.Merge
	}{q, m}
	_, err := c.do(ctx, "POST", "/v1/forge/merge", nil, body, nil, nil)
	return err
}

// Conversations lists threads, the most recently active first; next continues (before).
func (c *Client) Conversations(ctx context.Context, before string, limit int) ([]store.Conversation, string, error) {
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out struct {
		Items []store.Conversation `json:"items"`
		Next  string               `json:"next"`
	}
	_, err := c.do(ctx, "GET", "/v1/conversations", q, nil, &out, nil)
	return out.Items, out.Next, err
}

// AskingRequest is a permission request waiting for a person, with its message.
type AskingRequest struct {
	Message string `json:"message"`
	Thread  string `json:"thread,omitempty"`
	acpx.Request
}

// Asking lists the permission requests waiting (the node's user).
func (c *Client) Asking(ctx context.Context) ([]AskingRequest, error) {
	var out struct {
		Items []AskingRequest `json:"items"`
	}
	_, err := c.do(ctx, "GET", "/v1/permissions", nil, nil, &out, nil)
	return out.Items, err
}

// Answer answers a permission request of a message: an option it offered, or action "decline".
func (c *Client) Answer(ctx context.Context, msgID string, reply acpx.Reply) error {
	_, err := c.do(ctx, "POST", "/v1/messages/"+url.PathEscape(msgID)+"/permission", nil, reply, nil, nil)
	return err
}

// The bounds on reaching a node and on its health check; variables so tests need not wait them out.
var (
	dialTimeout      = 10 * time.Second
	handshakeTimeout = 10 * time.Second
	statusTimeout    = 30 * time.Second
	drainTimeout     = 2 * time.Second
)

// followRetry and followRetryMax pace Follow's reconnects.
var followRetry, followRetryMax = time.Second, 30 * time.Second

// unreachable is the error of a request that got no response: ctx's own when ctx ended (the
// caller gave up: not the node's fault), else ErrUnavailable.
func (c *Client) unreachable(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %s: %w", ErrUnavailable, c.URL, err)
}

// streamErrorEvent is the event a node's stream fails with (an event kind has no dash: an
// agent's `error` event is an event like any other).
const streamErrorEvent = "acpost-error"

// streamErr is the error an `event: acpost-error` of a stream carries: the node's own, which ends the
// stream for good, unless the node says it is unavailable (then reopening it may work).
func streamErr(data string) error {
	var eb struct {
		Status int   `json:"status"`
		Error  Error `json:"error"`
	}
	if json.Unmarshal([]byte(data), &eb) != nil || eb.Error.Code == "" {
		return fmt.Errorf("%w: a stream error: %.200s", ErrBadResponse, data)
	}
	eb.Error.Status = eb.Status
	if eb.Error.Code == "unavailable" {
		return fmt.Errorf("%w: %w", ErrUnavailable, &eb.Error)
	}
	return &eb.Error
}

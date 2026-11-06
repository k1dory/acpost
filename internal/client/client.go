// Package client talks to a node's HTTP API: the command line, a joined node and any Go program use it.
package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
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

	"github.com/k1dory/acpost/internal/store"
)

// SendRequest is the body of POST /v1/messages.
type SendRequest struct {
	To     string `json:"to"`
	Body   string `json:"body"`
	Task   string `json:"task,omitempty"`
	Thread string `json:"thread,omitempty"`
	// Session continues an existing ACP session of the agent.
	Session string            `json:"session,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
	Files   store.Files       `json:"files,omitempty"`
	TTL     string            `json:"ttl,omitempty"`
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
	tr := &http.Transport{MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 0}
	switch {
	case strings.HasPrefix(nodeURL, "unix://"):
		sock := strings.TrimPrefix(nodeURL, "unix://")
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
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
		rctx, done := context.WithTimeout(ctx, step+5*time.Second)
		if !deadline.IsZero() {
			done()
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

func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	_, err := c.do(ctx, "GET", "/v1/health", nil, nil, &s, nil)
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
func respErr(method, path string, resp *http.Response) *Error {
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
	for {
		m, done, err := c.followOnce(ctx, id, &after, fn)
		if done || err != nil && (ctx.Err() != nil || !errors.Is(err, ErrUnavailable)) {
			return m, err
		}
		select { // the connection dropped: again, from the last event seen
		case <-ctx.Done():
			return m, ctx.Err()
		case <-time.After(time.Second):
		}
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

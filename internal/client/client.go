// Package client talks to a node's HTTP API: the command line, a joined node and any Go program use it.
package client

import (
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
	TTL     string            `json:"ttl,omitempty"`
	// Set only by a node forwarding its own message to a hub.
	ID     string `json:"id,omitempty"`
	From   string `json:"from,omitempty"`
	Parent string `json:"parent,omitempty"`
	Depth  int    `json:"depth,omitempty"`
}

// ReplyRequest is the body of POST /v1/messages/{id}/reply.
type ReplyRequest struct {
	Lease string `json:"lease"`
	Body  string `json:"body,omitempty"`
	Error string `json:"error,omitempty"`
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

// Status is GET /v1/health.
type Status struct {
	OK      bool   `json:"ok"`
	Node    string `json:"node"`
	Version string `json:"version"`
	You     string `json:"you"`
	Hub     string `json:"hub,omitempty"`
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
		return 0, fmt.Errorf("%w: %s: %v", ErrUnavailable, c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var eb struct {
			Error Error `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if json.Unmarshal(b, &eb) != nil || eb.Error.Code == "" {
			eb.Error = Error{Code: "internal", Message: fmt.Sprintf("%s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(b)))}
		}
		eb.Error.Status = resp.StatusCode
		return resp.StatusCode, &eb.Error
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: bad response: %v", method, path, err)
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

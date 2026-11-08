package node

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// The cloud: a tenant of agent-sandbox. Its sandboxes are the node user's cloud chats; each is an
// agent of this node named by the sandbox's id (sb_…, driver sandbox), added when first written to;
// a new cloud task is a new sandbox of an agent of the tenant's catalogue.

type cloudAPI struct {
	url, token string
	hc         *http.Client
}

func newCloudAPI(c *config.Cloud) (*cloudAPI, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if c.CA != "" {
		pem, err := os.ReadFile(c.CA)
		if err != nil {
			return nil, fmt.Errorf("cloud.ca: %w", err)
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("cloud.ca: no certificate in %s", c.CA)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &cloudAPI{url: c.URL, token: c.Token, hc: &http.Client{Transport: tr, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *cloudAPI) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return errf("unavailable", "the cloud: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b[:min(len(b), 300)]))
		var e struct{ Error, Message string }
		if json.Unmarshal(b, &e) == nil && (e.Message != "" || e.Error != "") {
			msg = strings.TrimSpace(e.Error + " " + e.Message)
		}
		code := "unavailable"
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			code = "refused"
		case resp.StatusCode == 404:
			code = "not_found"
		case resp.StatusCode < 500:
			code = "invalid"
		}
		return errf(code, "the cloud: %s %s: %d %s", method, path, resp.StatusCode, msg)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return errf("unavailable", "the cloud: %s: not the API's answer (%v)", path, err)
		}
	}
	return nil
}

type cloudSandbox struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Repo         string    `json:"repo"`
	Status       string    `json:"status"`
	AgentRef     string    `json:"agent_ref"`
	LastActivity time.Time `json:"last_activity"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s cloudSandbox) chat() client.Chat {
	c := client.Chat{Key: "cloud:" + s.ID, Kind: "cloud", Agent: "agent:" + s.ID, Sandbox: s.ID, Title: s.Name,
		Repo: s.Repo, Status: s.Status, Updated: s.LastActivity}
	if c.Updated.IsZero() {
		c.Updated = s.CreatedAt
	}
	if ref := cmp.Or(s.AgentRef, s.Slug); ref != "" {
		c.CloudAgent = ref
	}
	return c
}

// cloudChats lists the tenant's sandboxes as chats (none without a cloud configured).
func (n *Node) cloudChats(ctx context.Context) ([]client.Chat, error) {
	if n.cloud == nil {
		return nil, nil
	}
	var list []cloudSandbox
	if err := n.cloud.call(ctx, "GET", "/v1/sandboxes", nil, &list); err != nil {
		return nil, err
	}
	out := make([]client.Chat, 0, len(list))
	for _, s := range list {
		out = append(out, s.chat())
	}
	return out, nil
}

// cloudAgent is a sandbox's agent, added on first use (verify: the sandbox is the tenant's).
func (n *Node) cloudAgent(name string, verify bool) (*sandboxAgent, error) {
	n.sbMu.Lock()
	sa := n.sandboxes[name]
	n.sbMu.Unlock()
	if sa != nil {
		return sa, nil
	}
	if n.cloud == nil {
		return nil, errf("not_found", "no cloud: cloud sandboxes need a cloud in the configuration")
	}
	if verify {
		var s cloudSandbox
		ctx, cancel := context.WithTimeout(n.ctx, 30*time.Second)
		err := n.cloud.call(ctx, "GET", "/v1/sandboxes/"+url.PathEscape(name), nil, &s)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	cfg, err := n.cfg.Cloud.CloudAgent(name)
	if err != nil {
		return nil, errf("invalid", "%v", err)
	}
	// The node's user and its own agents only: not a sender that comes through the hub.
	cfg.AllowFrom = []string{"user:*"} // a user is the node's own (its socket)
	for a := range n.cfg.Agents {
		cfg.AllowFrom = append(cfg.AllowFrom, "agent:"+a)
	}
	created, err := newSandboxAgent(n, name, cfg)
	if err != nil {
		return nil, errf("invalid", "%v", err)
	}
	n.sbMu.Lock()
	defer n.sbMu.Unlock()
	if sa := n.sandboxes[name]; sa != nil {
		return sa, nil
	}
	n.sandboxes[name] = created
	n.dynCfg[name] = cfg
	if n.started {
		created.start()
	}
	return created, nil
}

// CloudConfigured says whether the node has a cloud (an empty catalogue is not "no cloud").
func (n *Node) CloudConfigured() bool { return n.cloud != nil }

// CloudAgents lists the agents of the tenant's catalogue a cloud task can start from.
func (n *Node) CloudAgents(ctx context.Context, p Principal) ([]client.CloudAgent, error) {
	if err := userOnly(p, "the cloud"); err != nil {
		return nil, err
	}
	if n.cloud == nil {
		return []client.CloudAgent{}, nil
	}
	var items []struct {
		Name        string `json:"name"`
		Revision    int    `json:"revision"`
		Description string `json:"description"`
		Tenant      string `json:"tenant"`
	}
	if err := n.cloud.call(ctx, "GET", "/v1/agents", nil, &items); err != nil {
		return nil, err
	}
	out := make([]client.CloudAgent, 0, len(items))
	for _, it := range items {
		ref := it.Name
		if it.Tenant == "" {
			ref = "shared/" + it.Name
		}
		out = append(out, client.CloudAgent{Ref: ref, Name: it.Name, Description: it.Description, Revision: it.Revision})
	}
	return out, nil
}

var dnsLabel = regexp.MustCompile(`[^a-z0-9-]+`)

// CreateCloudTask makes a sandbox of a catalogue agent for a new cloud task; the task's messages go
// to its agent (agent:<sandbox id>).
func (n *Node) CreateCloudTask(ctx context.Context, p Principal, r client.CloudTask) (client.Chat, error) {
	if err := userOnly(p, "the cloud"); err != nil {
		return client.Chat{}, err
	}
	if n.cloud == nil {
		return client.Chat{}, errf("not_found", "no cloud in the configuration")
	}
	if r.Agent == "" || len(r.Agent) > 128 {
		return client.Chat{}, errf("invalid", "the catalogue's agent to start from")
	}
	name := strings.Trim(dnsLabel.ReplaceAllString(strings.ToLower(r.Name), "-"), "-")
	if len(name) > 40 {
		name = strings.Trim(name[:40], "-")
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	if r.Idem != "" {
		// A retry names the same sandbox: the one made the first time (its answer lost, or what
		// came after it failed) is found and taken, not made again.
		if len(r.Idem) > maxLabel {
			return client.Chat{}, errf("invalid", "the idempotency key is %d bytes, over %d", len(r.Idem), maxLabel)
		}
		// The name carries 128 bits of the key (the task's own words cut shorter to fit a DNS
		// label): two operations never take each other's sandbox.
		sum := sha256.Sum256([]byte(n.login + "\x00" + r.Idem))
		b = sum[:16]
		if len(name) > 24 {
			name = strings.Trim(name[:24], "-")
		}
		mu, _ := n.cloudOps.LoadOrStore(r.Idem, &sync.Mutex{})
		mu.(*sync.Mutex).Lock()
		defer mu.(*sync.Mutex).Unlock()
	}
	name = strings.TrimPrefix(cmp.Or(name, "task")+"-"+hex.EncodeToString(b), "-")
	var s cloudSandbox
	if r.Idem != "" {
		var list []cloudSandbox
		if err := n.cloud.call(ctx, "GET", "/v1/sandboxes", nil, &list); err != nil {
			return client.Chat{}, err
		}
		for _, it := range list {
			if it.Name == name && cmp.Or(it.AgentRef, it.Slug) == r.Agent {
				s = it
			}
		}
	}
	if s.ID == "" {
		body := map[string]any{"name": name, "owner": n.login, "agent_ref": r.Agent}
		if r.Repo != "" {
			body["repo"] = r.Repo
		}
		if err := n.cloud.call(ctx, "POST", "/v1/sandboxes", body, &s); err != nil {
			return client.Chat{}, err
		}
	}
	if !config.ValidName(s.ID) {
		return client.Chat{}, errf("unavailable", "the cloud named the sandbox %q", s.ID)
	}
	if _, err := n.cloudAgent(s.ID, false); err != nil {
		return client.Chat{}, err
	}
	n.forgetChats()
	return s.chat(), nil
}

// CloudHistory reads a sandbox's conversation as events: its user's messages, the agent's text,
// tool calls, requests and run boundaries (thoughts and plans too, from the harness's lines).
func (n *Node) CloudHistory(ctx context.Context, p Principal, id string) ([]store.Event, error) {
	if err := userOnly(p, "the cloud"); err != nil {
		return nil, err
	}
	if !config.ValidName(id) || !strings.HasPrefix(id, "sb_") {
		return nil, errf("invalid", "a sandbox id (sb_…)")
	}
	sa, err := n.cloudAgent(id, true)
	if err != nil {
		return nil, err
	}
	var out []store.Event
	add := func(at time.Time, kind string, data any) {
		if len(out) < historyEvents {
			out = append(out, store.Event{Seq: int64(len(out) + 1), Time: at, Kind: kind, Data: eventJSON(data)})
		}
	}
	after := int64(0)
	for range 1000 {
		next, err := sa.page(ctx, after, func(sm sandboxMessage) bool {
			at := sm.At
			switch sm.Role {
			case "user":
				add(at, "user", map[string]string{"text": sm.Text})
			case "assistant":
				add(at, "text", map[string]string{"text": sm.Text + "\n\n"})
			case "tool":
				add(at, "tool_call", toolEvent{ID: "sandbox-" + fmt.Sprint(sm.ID), Title: redact(cutString(sm.Text, 512)), Status: "completed"})
			case "system":
				add(at, "notice", map[string]string{"text": sm.Text})
			case "log":
				var l struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Entries []struct {
						Content string `json:"content"`
						Status  string `json:"status"`
					} `json:"entries"`
				}
				if json.Unmarshal([]byte(sm.Text), &l) == nil {
					switch l.Type {
					case "acp.thought":
						add(at, "thought", map[string]string{"text": l.Text})
					case "acp.plan":
						var es []string
						for _, e := range l.Entries {
							es = append(es, e.Status+": "+e.Content)
						}
						add(at, "plan", es)
					}
				}
			}
			return true
		})
		if err != nil {
			return nil, err
		}
		if next <= after {
			break
		}
		after = next
	}
	if out == nil {
		out = []store.Event{}
	}
	return out, nil
}

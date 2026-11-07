// Package forge reads and opens pull requests, CI runs and their logs on the forge a repository's
// remote names: GitHub (github.com and Enterprise), GitLab (gitlab.com and self-managed) and
// Forgejo or Gitea. Which one a self-hosted host is, and its version, is asked of the host.
package forge

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kinds of forge.
const (
	GitHub  = "github"
	GitLab  = "gitlab"
	Forgejo = "forgejo" // Forgejo or Gitea: one API
)

// Repo is a repository on a forge.
type Repo struct {
	Host string `json:"host"`           // host[:port] as the remote names it
	Path string `json:"path"`           // owner/name (GitLab: group/sub/name)
	Web  string `json:"web"`            // https://host/path
	Kind string `json:"kind,omitempty"` // github, gitlab, forgejo
}

var (
	scpLike     = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)
	repoSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	hostName    = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}(:[0-9]{1,5})?$`)
)

// ParseRemote reads a git remote URL (https://host/o/r.git, ssh://git@host:22/o/r, git@host:o/r).
func ParseRemote(remote string) (Repo, error) {
	remote = strings.TrimSpace(remote)
	var host, path string
	if u, err := url.Parse(remote); err == nil && u.Scheme != "" && u.Host != "" {
		if u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
			return Repo{}, fmt.Errorf("%s: a remote with a query, fragment or escapes in its path", safeRemote(remote))
		}
		host, path = u.Host, u.Path
		if u.Scheme == "ssh" || u.Scheme == "git" {
			host = u.Hostname() // an ssh port is not the web's
		}
	} else if m := scpLike.FindStringSubmatch(remote); m != nil {
		host, path = m[1], m[2]
	} else {
		return Repo{}, fmt.Errorf("%s: not a remote URL", safeRemote(remote))
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if strings.Count(path, "/") < 1 || host == "" {
		return Repo{}, fmt.Errorf("%s: no owner/name in it", safeRemote(remote))
	}
	// The path goes into API paths: plain names only (no %, #, ?, .., spaces).
	for _, seg := range strings.Split(path, "/") {
		if !repoSegment.MatchString(seg) || seg == "." || seg == ".." {
			return Repo{}, fmt.Errorf("%s: a part of its path is not a plain owner or repository name", safeRemote(remote))
		}
	}
	if !hostName.MatchString(host) {
		return Repo{}, fmt.Errorf("%s: its host is not a host name", safeRemote(remote))
	}
	return Repo{Host: host, Path: path, Web: "https://" + host + "/" + path}, nil
}

// Server is what a host says it is.
type Server struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	API     string `json:"api"` // the API's base URL
}

// Client talks to forges.
type Client struct {
	HTTP *http.Client
	// Token is the token for a host, "" for none.
	Token func(host string) string
	// Kind is the kind configured for a host, "" to ask the host.
	Kind func(host string) string
}

// Error is a forge's refusal, or a failure to reach it.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// hc is the HTTP client: a redirect keeps to https, and takes the token only to the same origin
// (scheme, host and port); elsewhere (GitHub's log storage) it goes without.
func (c *Client) hc() *http.Client {
	base := &http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		cp := *c.HTTP
		base = &cp
	}
	base.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return redirectRefused{"a redirect to an address that is not https"} // nothing of the address
		}
		if req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization")
			req.Header.Del("PRIVATE-TOKEN")
		}
		return nil
	}
	return base
}

func (c *Client) token(host string) string {
	if c.Token == nil {
		return ""
	}
	return c.Token(host)
}

// auth sets a request's authorization the forge's way.
func auth(req *http.Request, kind, token string) {
	if token == "" {
		return
	}
	switch kind {
	case GitHub:
		req.Header.Set("Authorization", "Bearer "+token)
	case GitLab:
		req.Header.Set("PRIVATE-TOKEN", token)
	default:
		req.Header.Set("Authorization", "token "+token)
	}
}

// get reads a forge's JSON (into out) or text (out a *string), at most max bytes.
func (c *Client) get(ctx context.Context, srv Server, host, path string, out any, max int64) (http.Header, error) {
	return c.do(ctx, "GET", srv, host, path, nil, out, max)
}

func (c *Client) do(ctx context.Context, method string, srv Server, host, path string, body, out any, max int64) (http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, srv.API+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "acpost")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if srv.Kind == GitHub {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	// The token this request carries: said nowhere — whatever the server or the transport repeats
	// (a body, a redirect's URL) is masked before it is cut or shown.
	tok := c.token(host)
	redact := func(s string) string {
		if tok == "" {
			return s
		}
		return strings.ReplaceAll(s, tok, "•••")
	}
	auth(req, srv.Kind, tok)
	resp, err := c.hc().Do(req)
	if err != nil {
		// Said in acpost's own words: Go's errors quote URLs, a redirect's may carry credentials
		// (GitHub's log links do), a malformed one whole.
		return nil, &Error{Message: fmt.Sprintf("%s %s %s: %s", host, method, path, transportWhy(err))}
	}
	defer resp.Body.Close()
	if t, ok := out.(*tailText); ok && resp.StatusCode < 400 {
		if err := t.read(resp.Body, max); err != nil {
			return resp.Header, &Error{Status: resp.StatusCode, Message: fmt.Sprintf("%s %s: the answer broke off (%s)", host, path, transportWhy(err))}
		}
		return resp.Header, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return resp.Header, &Error{Status: resp.StatusCode, Message: fmt.Sprintf("%s %s: the answer broke off (%s)", host, path, transportWhy(err))}
	}
	if resp.StatusCode >= 400 {
		whole := redact(string(b))
		var e struct{ Message string }
		msg := ""
		// The API's own message only (decoded, so masked after): a raw body is not shown, it may say
		// anything in any encoding.
		if json.Unmarshal([]byte(whole), &e) == nil && e.Message != "" {
			msg = redact(e.Message)
		} else {
			msg = http.StatusText(resp.StatusCode)
		}
		if tok != "" && len(b) > int(max) {
			msg = "(an answer too long to show)" // the token may cross the cut: not shown at all
		} else if r := []rune(msg); len(r) > 300 {
			msg = string(r[:300]) + "…"
		}
		return resp.Header, &Error{Status: resp.StatusCode, Message: fmt.Sprintf("%s %s %s: %s", host, method, path, msg)}
	}
	if int64(len(b)) > max {
		b = b[:max]
	}
	switch o := out.(type) {
	case nil:
	case *string:
		*o = string(b)
	default:
		if err := json.Unmarshal(b, out); err != nil {
			return resp.Header, &Error{Status: resp.StatusCode, Message: redact(fmt.Sprintf("%s %s: not the API's answer (%v)", host, path, err))}
		}
	}
	return resp.Header, nil
}

// Probe finds what forge a host is and its version: github.com; a configured kind; else the
// host's own answer (Forgejo/Gitea /api/v1/version, GitLab /api/v4/version, GitHub Enterprise's
// X-GitHub-Enterprise-Version).
func (c *Client) Probe(ctx context.Context, host string) (Server, error) {
	kind := ""
	if c.Kind != nil {
		kind = c.Kind(host)
	}
	if host == "github.com" || kind == GitHub {
		srv := Server{Kind: GitHub, API: "https://api.github.com"}
		if host != "github.com" {
			srv.API = "https://" + host + "/api/v3"
		}
		var meta map[string]any
		h, err := c.get(ctx, srv, host, "/meta", &meta, 1<<20)
		if err == nil && h.Get("X-GitHub-Enterprise-Version") != "" {
			srv.Version = h.Get("X-GitHub-Enterprise-Version")
		}
		if err != nil && host != "github.com" {
			return srv, err
		}
		return srv, nil
	}
	try := []string{Forgejo, GitLab}
	if kind != "" {
		try = []string{kind}
	}
	var errs []error
	for _, k := range try {
		srv := Server{Kind: k, API: "https://" + host + "/api/v1"}
		var v struct {
			Version string `json:"version"`
		}
		if k == GitLab {
			srv.API = "https://" + host + "/api/v4"
		}
		_, err := c.get(ctx, srv, host, "/version", &v, 1<<16)
		var fe *Error
		if k == Forgejo && errors.As(err, &fe) && fe.Status == http.StatusUnauthorized {
			// Forgejo refuses even its public /version with a bad token: asked again without it, so a
			// wrong token is told as one, not as an unknown server.
			_, err = c.anonymous().get(ctx, srv, host, "/version", &v, 1<<16)
		}
		if err == nil && v.Version != "" {
			srv.Version = v.Version
			return srv, nil
		}
		if k == GitLab && errors.As(err, &fe) && fe.Status == http.StatusUnauthorized {
			return srv, nil // GitLab answers /version only with a token: it is GitLab all the same
		}
		errs = append(errs, err)
	}
	if kind == "" { // GitHub Enterprise: /api/v3/meta says its version in a header
		srv := Server{Kind: GitHub, API: "https://" + host + "/api/v3"}
		var meta map[string]any
		h, err := c.get(ctx, srv, host, "/meta", &meta, 1<<20)
		if err == nil && h.Get("X-GitHub-Enterprise-Version") != "" {
			srv.Version = h.Get("X-GitHub-Enterprise-Version")
			return srv, nil
		}
		errs = append(errs, err)
	}
	return Server{}, fmt.Errorf("%s: not a forge acpost knows (GitHub, GitLab, Forgejo/Gitea): %w", host, errors.Join(errs...))
}

// redirectRefused is a redirect hc does not follow, said without its URL.
type redirectRefused struct{ why string }

func (r redirectRefused) Error() string { return r.why }

// transportWhy says why a request got no answer, in words that hold nothing of the request.
func transportWhy(err error) string {
	var rr redirectRefused
	var cv *tls.CertificateVerificationError
	var ue *url.Error
	var ne net.Error
	var op *net.OpError
	switch {
	case errors.As(err, &rr):
		return rr.why
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "no answer in time"
	case errors.As(err, &cv):
		return "its certificate is not trusted"
	case errors.As(err, &op):
		return "cannot connect (" + op.Op + ")"
	case errors.As(err, &ue) && strings.Contains(ue.Err.Error(), "redirect"):
		return "a redirect it could not follow"
	default:
		return "the request failed"
	}
}

// safeRemote is a remote as an error may quote it: scheme, host and path, no user, password,
// query or fragment ("the remote" when it does not parse as a URL).
func safeRemote(remote string) string {
	if u, err := url.Parse(remote); err == nil && u.Host != "" && !strings.ContainsAny(u.Host, "@") {
		return strconv.Quote((&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String())
	}
	if at := strings.LastIndex(remote, "@"); at >= 0 && !strings.Contains(remote, "://") {
		rest := remote[at+1:] // scp form, user@host:path: without the user, query or fragment
		if i := strings.IndexAny(rest, "?#"); i >= 0 {
			rest = rest[:i]
		}
		return strconv.Quote(rest)
	}
	return "the remote"
}

// anonymous is the client without tokens.
func (c *Client) anonymous() *Client {
	a := *c
	a.Token = func(string) string { return "" }
	return &a
}

// Pull is a pull (merge) request.
type Pull struct {
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	State   string    `json:"state"` // open, closed, merged
	Draft   bool      `json:"draft,omitempty"`
	Author  string    `json:"author"`
	Head    string    `json:"head"` // branch
	Base    string    `json:"base"`
	URL     string    `json:"url"`
	Updated time.Time `json:"updated"`
}

// Check is a CI run (GitHub Actions run, GitLab pipeline, Forgejo/Gitea commit status) and its jobs.
type Check struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"` // queued, running, success, failure, cancelled, skipped…
	URL        string    `json:"url,omitempty"`
	Ref        string    `json:"ref,omitempty"`
	SHA        string    `json:"sha,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	Finished   time.Time `json:"finished,omitempty"`
	Jobs       []Job     `json:"jobs,omitempty"`
	LogsByJobs bool      `json:"logs_by_jobs,omitempty"` // logs are read per job
}

// Job is a job of a run.
type Job struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	URL      string    `json:"url,omitempty"`
	Started  time.Time `json:"started,omitempty"`
	Finished time.Time `json:"finished,omitempty"`
}

// tailText is text read for its end: past max bytes, its start goes (said in Cut).
type tailText struct {
	Text string
	Cut  bool
}

func (t *tailText) read(r io.Reader, max int64) error {
	buf := make([]byte, 0, min(max, 1<<20))
	chunk := make([]byte, 64<<10)
	for {
		k, err := r.Read(chunk)
		buf = append(buf, chunk[:k]...)
		if int64(len(buf)) > 2*max {
			buf = append(buf[:0], buf[int64(len(buf))-max:]...)
			t.Cut = true
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if int64(len(buf)) > max {
		buf, t.Cut = buf[int64(len(buf))-max:], true
	}
	t.Text = string(buf)
	return nil
}

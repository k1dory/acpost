package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// Handoff: a task begun on this Mac goes on in the cloud. The node starts a sandbox of the
// tenant's catalogue on the repository's remote and sends it what it needs to carry on: the
// branch and the commit to start from, the work not on the remote yet as a patch (secrets left
// out), and the conversation so far.

const (
	handoffTalk  = 48 << 10 // the conversation's text, at most (its end kept)
	handoffPatch = 8 << 20  // the patch, at most
)

// secretPath: files a handoff never sends (their names are said instead).
var secretPaths = []string{".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "id_rsa*", "id_ed25519*", "*.keystore", ".netrc", "credentials*", "*secret*", "*.tfvars", "*.kubeconfig", "kubeconfig"}

// HandoffRequest is a chat to go on in the cloud.
type HandoffRequest struct {
	Thread     string `json:"thread,omitempty"`  // an acpost thread
	Agent      string `json:"agent,omitempty"`   // or an agent's session
	Session    string `json:"session,omitempty"` //
	Cwd        string `json:"cwd,omitempty"`     // its directory (the repository)
	CloudAgent string `json:"cloud_agent"`       // the catalogue's agent to start from
	Note       string `json:"note,omitempty"`    // what to do next, said by the user
	Idem       string `json:"idem,omitempty"`    // the same for a retry: the sandbox and message of the first are the answer
}

// HandoffResult is the cloud chat the task goes on in, and what went with it.
type HandoffResult struct {
	Chat    client.Chat `json:"chat"`
	Message string      `json:"message"`
	Repo    string      `json:"repo"`
	Branch  string      `json:"branch,omitempty"`
	Base    string      `json:"base,omitempty"`
	Patch   int         `json:"patch"`              // bytes
	Left    []string    `json:"left_out,omitempty"` // files not sent (secrets, too big)
}

// repoState is what a handoff learns of a working tree.
type repoState struct {
	remote, branch, base, head string
	patch                      []byte
	left                       []string
}

// Handoff sends a local chat on to the cloud.
func (n *Node) Handoff(ctx context.Context, p Principal, r HandoffRequest) (HandoffResult, error) {
	var out HandoffResult
	if err := userOnly(p, "a handoff"); err != nil {
		return out, err
	}
	if n.cloud == nil {
		return out, errh("not_found", "name an agent-sandbox tenant as cloud: in the node's configuration", "no cloud is configured")
	}
	if r.CloudAgent == "" {
		return out, errf("invalid", "the cloud agent to go on with")
	}
	if len(r.Idem) > maxLabel-len("handoff:") {
		return out, errf("invalid", "the idempotency key is %d bytes, over %d", len(r.Idem), maxLabel-len("handoff:"))
	}
	if r.Cwd == "" {
		return out, errf("invalid", "the chat's directory: a git repository")
	}
	dir, err := checkDir(r.Cwd)
	if err != nil {
		return out, err
	}
	rs, err := readRepo(ctx, dir)
	if err != nil {
		return out, err
	}
	talk, err := n.handoffTalk(ctx, p, r)
	if err != nil {
		return out, err
	}

	// What goes is checked before a sandbox is made for it: a message refused after would leave a
	// sandbox with nothing to do.
	body := handoffBody(rs, talk, r.Note)
	req := SendRequest{Body: body, Meta: map[string]string{"handoff": "1"}}
	if len(rs.patch) > 0 {
		req.Files = []store.File{{Name: "handoff.patch", Type: "text/x-diff", Data: rs.patch}}
	}
	if len(body) > int(n.cfg.Limits.MaxBody) {
		return out, errh("refused", "hand off a shorter conversation, or raise limits.max_body", "the handoff is %d bytes, over limits.max_body (%d)", len(body), n.cfg.Limits.MaxBody)
	}
	if err := n.checkFiles(req.Files, nil, "the handoff"); err != nil {
		return out, err
	}
	task := client.CloudTask{Agent: r.CloudAgent, Repo: rs.remote, Name: path.Base(dir)}
	if r.Idem != "" {
		task.Idem, req.IdemKey = "handoff:"+r.Idem, "handoff:"+r.Idem
	}
	chat, err := n.CreateCloudTask(ctx, p, task)
	if err != nil {
		return out, err
	}
	req.To = chat.Agent
	m, _, err := n.Send(ctx, p, req)
	if err != nil {
		return out, err
	}
	return HandoffResult{Chat: chat, Message: m.ID, Repo: rs.remote, Branch: rs.branch, Base: rs.base, Patch: len(rs.patch), Left: rs.left}, nil
}

func handoffBody(rs repoState, talk, note string) string {
	var b strings.Builder
	b.WriteString("You are continuing a task the user started on their Mac with a local agent. ")
	fmt.Fprintf(&b, "The repository is %s.\n\n", rs.remote)
	switch {
	case rs.base != "":
		fmt.Fprintf(&b, "Start from commit %s", rs.base)
		if rs.branch != "" {
			fmt.Fprintf(&b, " (the user's branch is %s)", rs.branch)
		}
		b.WriteString(": `git fetch origin && git checkout -b " + handoffBranch(rs.branch) + " " + rs.base + "`.")
	default:
		fmt.Fprintf(&b, "Start from the default branch. The Mac's HEAD (%s) may not be on the remote.", rs.head)
	}
	if len(rs.patch) > 0 {
		b.WriteString(" The work done since — commits not on the remote and changes not committed — is attached as handoff.patch: apply it with `git apply --index handoff.patch` before anything else.")
	}
	if len(rs.left) > 0 {
		fmt.Fprintf(&b, " Left out of the patch on purpose (secrets or too big): %s — ask the user if you need them.", strings.Join(rs.left, ", "))
	}
	b.WriteString("\n\n")
	if talk != "" {
		b.WriteString("The conversation so far (the user's messages and the local agent's answers):\n\n<conversation>\n" + talk + "\n</conversation>\n\n")
	}
	if strings.TrimSpace(note) != "" {
		b.WriteString("What the user wants now: " + strings.TrimSpace(note) + "\n")
	} else {
		b.WriteString("Carry on with the task from where the conversation stopped. Open a pull request when it is done.\n")
	}
	return b.String()
}

// gitSafe are settings a handoff's git runs with: nothing of the repository's configuration
// runs a program (text conversion, external diff, fsmonitor), paths come as they are.
var gitSafe = []string{"-c", "core.quotepath=off", "-c", "core.fsmonitor=false", "-c", "diff.external=", "-c", "core.hooksPath=/dev/null"}

// handoffBranch is a branch name safe to put in a command: the user's, its odd characters dashes.
func handoffBranch(b string) string {
	b = branchOdd.ReplaceAllString(b, "-")
	b = strings.Trim(b, "-./")
	if b == "" {
		return "handoff"
	}
	return "handoff/" + b
}

var branchOdd = regexp.MustCompile(`[^A-Za-z0-9._/-]+`)

// readRepo learns the remote, the branch, the base on the remote and the patch from it — for the
// whole repository dir is in.
func readRepo(ctx context.Context, dir string) (repoState, error) {
	var rs repoState
	// git as snapshots run it (changes.go): hooks, fsmonitor, external diff off, every filter driver a
	// pass-through, the process group killed past its time — and the output bounded.
	run := func(in string, args ...string) ([]byte, error) {
		out, cut, err := git(ctx, in, nil, handoffPatch+1, append(append([]string{}, gitSafe...), args...)...)
		if cut {
			return out, errf("invalid", "the work not on the remote is over %d MiB: commit and push it, then hand off", handoffPatch>>20)
		}
		return out, err
	}
	word := func(in string, args ...string) (string, error) { // a scalar result
		b, err := run(in, args...)
		return strings.TrimSpace(string(b)), err
	}
	top, err := word(dir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return rs, errf("invalid", "%s is not in a git repository: the cloud starts from a repository's remote", dir)
	}
	remote, err := word(top, "remote", "get-url", "origin")
	if err != nil || remote == "" {
		return rs, errh("invalid", "add a remote (git remote add origin URL) and push", "the repository has no remote origin: the cloud has nothing to clone")
	}
	if rs.remote, err = plainRemote(remote); err != nil {
		return rs, err
	}
	rs.branch, _ = word(top, "rev-parse", "--abbrev-ref", "HEAD")
	if rs.branch == "HEAD" {
		rs.branch = ""
	}
	rs.head, _ = word(top, "rev-parse", "--short", "HEAD")
	// The base: where this work leaves what the remote has (the branch's upstream, else origin's
	// default branch). None known: the work may be on this Mac only — say so, do not guess.
	// Only origin's refs: the cloud clones origin (an upstream elsewhere, or a local one, has commits
	// it cannot see).
	refs := []string{"origin/HEAD"}
	if up, err := word(top, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil && strings.HasPrefix(up, "origin/") {
		refs = append([]string{up}, refs...)
	}
	for _, ref := range refs {
		if b, err := word(top, "merge-base", "HEAD", "refs/remotes/"+ref); err == nil && gitObject.MatchString(b) {
			rs.base = b
			break
		}
	}
	if rs.base == "" {
		return rs, errh("invalid", "push the branch (git push -u origin "+cmpOr(rs.branch, "HEAD")+") or set origin's default (git remote set-head origin -a)",
			"no commit of this branch is known to be on the remote: what to start from cannot be told")
	}

	// The paths changed since the base (tracked) and the new ones: secrets — by any part of their
	// path — named and left out, the rest in the patch, byte for byte.
	names, err := run(top, "diff", "--name-only", "-z", "--no-renames", rs.base, "--")
	if err != nil {
		if CodeOf(err) == "invalid" {
			return rs, err
		}
		return rs, errf("unavailable", "git diff failed in %s", top)
	}
	var send []string
	for _, f := range strings.Split(string(names), "\x00") {
		switch {
		case f == "":
		case secretPath(f):
			rs.left = append(rs.left, f)
		default:
			send = append(send, f)
		}
	}
	var patch bytes.Buffer
	for len(send) > 0 { // in groups: a long list of paths
		k := min(len(send), 200)
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--binary", rs.base, "--"}
		for _, f := range send[:k] {
			args = append(args, ":(literal)"+f)
		}
		d, err := run(top, args...)
		if err != nil {
			if CodeOf(err) == "invalid" {
				return rs, err
			}
			return rs, errf("unavailable", "git diff failed in %s", top)
		}
		patch.Write(d)
		send = send[k:]
	}
	untracked, _ := run(top, "ls-files", "--others", "--exclude-standard", "-z")
	for _, f := range strings.Split(string(untracked), "\x00") {
		if f == "" {
			continue
		}
		if secretPath(f) {
			rs.left = append(rs.left, f)
			continue
		}
		// A new file as a patch (git diff --no-index exits 1 when there is a difference).
		o, err := run(top, "diff", "--no-index", "--binary", "--no-ext-diff", "--no-textconv", "--", "/dev/null", f)
		if err != nil && CodeOf(err) == "invalid" {
			rs.left = append(rs.left, f) // too big to send
			continue
		}
		if patch.Len()+len(o) > handoffPatch {
			rs.left = append(rs.left, f)
			continue
		}
		patch.Write(o)
	}
	if patch.Len() > handoffPatch {
		return rs, errh("invalid", "commit and push the work, then hand off", "the work not on the remote is %d MiB, over %d MiB", patch.Len()>>20, handoffPatch>>20)
	}
	rs.patch = patch.Bytes()
	return rs, nil
}

// secretPath: a path a handoff does not send — when any part of it (a file, or a directory it is
// in) has a secret's name.
func secretPath(p string) bool {
	for _, part := range strings.Split(p, "/") {
		for _, s := range secretPaths {
			if ok, _ := path.Match(strings.ToLower(s), strings.ToLower(part)); ok {
				return true
			}
		}
	}
	return false
}

var scpRemote = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?[A-Za-z0-9.-]+:[^/\\][^\s]*$|^(?:[A-Za-z0-9._-]+@)?[A-Za-z0-9.-]+:/[^\s]*$`)

// plainRemote is a remote as the cloud gets it: an URL without a password (an ssh user kept, an
// http one dropped), or the scp form (user@host:path); anything else is refused, not passed on.
func plainRemote(s string) (string, error) {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", errf("invalid", "the remote origin is not a URL acpost can read")
		}
		switch u.Scheme {
		case "ssh", "git+ssh":
			if u.User != nil {
				u.User = url.User(u.User.Username())
			}
		case "https", "http", "git":
			u.User = nil
		default:
			return "", errf("invalid", "the remote origin's scheme %s: https or ssh", u.Scheme)
		}
		u.RawQuery, u.Fragment = "", ""
		return u.String(), nil
	}
	if scpRemote.MatchString(s) {
		return s, nil
	}
	return "", errf("invalid", "the remote origin is not a URL acpost can read")
}

// handoffTalk is the conversation so far as text: the user's messages and the agent's answers,
// its end kept within handoffTalk.
func (n *Node) handoffTalk(ctx context.Context, p Principal, r HandoffRequest) (string, error) {
	var parts []string
	switch {
	case r.Thread != "":
		// All of the thread (oldest first, page by page), its last 200 kept.
		var page []store.Message
		after := ""
		for i := 0; i < 50; i++ {
			got, next, err := n.st.List(ctx, store.Filter{Thread: r.Thread, After: after, Limit: 200})
			if err != nil {
				return "", errf("unavailable", "%v", err)
			}
			page = append(page, got...)
			if len(page) > 200 {
				page = page[len(page)-200:]
			}
			if next == "" {
				break
			}
			after = next
		}
		for _, m := range page {
			full, err := n.st.Get(ctx, m.ID)
			if err != nil {
				continue
			}
			parts = append(parts, "User: "+strings.TrimSpace(full.Body))
			if strings.TrimSpace(full.Reply) != "" {
				parts = append(parts, "Agent: "+strings.TrimSpace(full.Reply))
			}
		}
	case r.Agent != "" && r.Session != "":
		evs, err := n.SessionHistory(ctx, p, strings.TrimPrefix(r.Agent, "agent:"), r.Session, r.Cwd)
		if err != nil {
			return "", err
		}
		var who, cur string
		flush := func() {
			if strings.TrimSpace(cur) != "" {
				parts = append(parts, who+": "+strings.TrimSpace(cur))
			}
			cur = ""
		}
		for _, e := range evs {
			var d struct {
				Text string `json:"text"`
			}
			if e.Kind != "user" && e.Kind != "text" {
				continue
			}
			_ = json.Unmarshal([]byte(e.Data), &d)
			w := "Agent"
			if e.Kind == "user" {
				w = "User"
			}
			if w != who {
				flush()
				who = w
			}
			cur += d.Text
		}
		flush()
	}
	text := strings.Join(parts, "\n\n")
	if len(text) > handoffTalk {
		text = "(earlier part left out)\n…" + text[len(text)-handoffTalk:]
	}
	return text, nil
}

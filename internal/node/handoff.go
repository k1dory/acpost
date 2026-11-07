package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"strings"
	"time"

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
	unpushed                   bool // no remote-tracking base: commits may be on this Mac only
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

	chat, err := n.CreateCloudTask(ctx, p, client.CloudTask{Agent: r.CloudAgent, Repo: rs.remote, Name: path.Base(dir)})
	if err != nil {
		return out, err
	}
	body := handoffBody(rs, talk, r.Note)
	req := SendRequest{To: chat.Agent, Body: body, Meta: map[string]string{"handoff": "1"}}
	if len(rs.patch) > 0 {
		req.Files = []store.File{{Name: "handoff.patch", Type: "text/x-diff", Data: rs.patch}}
	}
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
		b.WriteString(": `git fetch origin && git checkout -b " + cmpOr(rs.branch, "handoff") + " " + rs.base + "`.")
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

// readRepo learns the remote, the branch, the base on the remote and the patch from it.
func readRepo(ctx context.Context, dir string) (repoState, error) {
	var rs repoState
	git := func(args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(c, "git", append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)...)
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		err := cmd.Run()
		return strings.TrimSpace(o.String()), err
	}
	if top, err := git("rev-parse", "--show-toplevel"); err != nil || top == "" {
		return rs, errf("invalid", "%s is not in a git repository: the cloud starts from a repository's remote", dir)
	}
	remote, err := git("remote", "get-url", "origin")
	if err != nil || remote == "" {
		return rs, errh("invalid", "add a remote (git remote add origin URL) and push", "the repository has no remote origin: the cloud has nothing to clone")
	}
	rs.remote = plainRemote(remote)
	rs.branch, _ = git("rev-parse", "--abbrev-ref", "HEAD")
	if rs.branch == "HEAD" {
		rs.branch = ""
	}
	rs.head, _ = git("rev-parse", "--short", "HEAD")
	// The base: where this work leaves what the remote has (the branch's upstream, else origin's
	// default branch).
	for _, ref := range []string{"@{u}", "origin/HEAD"} {
		if b, err := git("merge-base", "HEAD", ref); err == nil && b != "" {
			rs.base = b
			break
		}
	}
	rs.unpushed = rs.base == ""
	from := cmpOr(rs.base, "HEAD")

	// The patch: from the base to the working tree, tracked files and new ones, secrets left out.
	excl := []string{"--", "."}
	for _, s := range secretPaths {
		excl = append(excl, ":(exclude,glob)**/"+s, ":(exclude,glob)"+s)
	}
	diff, err := git(append([]string{"diff", "--no-ext-diff", "--no-textconv", "--binary", from}, excl...)...)
	if err != nil {
		return rs, errf("unavailable", "git diff failed in %s", dir)
	}
	var patch strings.Builder
	if diff != "" {
		patch.WriteString(diff + "\n")
	}
	untracked, _ := git("ls-files", "--others", "--exclude-standard", "-z")
	for _, f := range strings.Split(untracked, "\x00") {
		if f == "" {
			continue
		}
		if secretPath(f) {
			rs.left = append(rs.left, f)
			continue
		}
		// A new file as a patch (git diff --no-index exits 1 when there is a difference).
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(c, "git", "-C", dir, "diff", "--no-index", "--binary", "--no-ext-diff", "--", "/dev/null", f)
		var o bytes.Buffer
		cmd.Stdout = &o
		_ = cmd.Run()
		cancel()
		if patch.Len()+o.Len() > handoffPatch {
			rs.left = append(rs.left, f)
			continue
		}
		patch.Write(o.Bytes())
	}
	// Tracked secrets changed since the base: named, not sent.
	if names, err := git("diff", "--name-only", from); err == nil {
		for _, f := range strings.Split(names, "\n") {
			if f != "" && secretPath(f) {
				rs.left = append(rs.left, f)
			}
		}
	}
	if patch.Len() > handoffPatch {
		return rs, errh("invalid", "commit and push the work, then hand off", "the work not on the remote is %d MiB, over %d MiB", patch.Len()>>20, handoffPatch>>20)
	}
	rs.patch = []byte(patch.String())
	return rs, nil
}

// secretPath: a path a handoff does not send.
func secretPath(p string) bool {
	base := path.Base(p)
	for _, s := range secretPaths {
		if ok, _ := path.Match(s, base); ok {
			return true
		}
	}
	return false
}

// plainRemote is a remote without credentials in it.
func plainRemote(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		u.User = nil
		u.RawQuery, u.Fragment = "", ""
		return u.String()
	}
	return s // scp form (git@host:path): the user is a name, not a secret
}

// handoffTalk is the conversation so far as text: the user's messages and the agent's answers,
// its end kept within handoffTalk.
func (n *Node) handoffTalk(ctx context.Context, p Principal, r HandoffRequest) (string, error) {
	var parts []string
	switch {
	case r.Thread != "":
		page, _, err := n.st.List(ctx, store.Filter{Thread: r.Thread, Limit: 200})
		if err != nil {
			return "", errf("unavailable", "%v", err)
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

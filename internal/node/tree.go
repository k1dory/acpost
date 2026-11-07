package node

import (
	"bytes"
	"context"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The message tree — what an agent asked other agents while working on a message, and what they
// asked in turn — and a file as a turn left it.

// TreeNode is a message under another: who asked whom, what, and how it ended.
type TreeNode struct {
	ID     string      `json:"id"`
	Parent string      `json:"parent"`
	Depth  int         `json:"depth"`
	From   string      `json:"from"`
	To     string      `json:"to"`
	Thread string      `json:"thread"`
	State  store.State `json:"state"`
	Body   string      `json:"body"`  // its start
	Reply  string      `json:"reply"` // its start
	At     time.Time   `json:"at"`
}

const treeText = 400

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > treeText {
		return string(r[:treeText]) + "…"
	}
	return s
}

// Tree is the messages under id (not id itself) the principal may see, in the order they came.
func (n *Node) Tree(ctx context.Context, p Principal, id string) ([]TreeNode, error) {
	if _, err := n.Get(ctx, p, id); err != nil {
		return nil, err
	}
	ids, err := n.st.Descendants(ctx, id, 500)
	if err != nil {
		return nil, errf("unavailable", "%v", err)
	}
	out := []TreeNode{}
	for _, d := range ids {
		if d == id {
			continue
		}
		m, err := n.st.Get(ctx, d)
		if err != nil || !n.canSee(p, m) {
			continue
		}
		out = append(out, TreeNode{ID: m.ID, Parent: m.Parent, Depth: m.Depth, From: m.From, To: m.To, Thread: cmpOr(m.Thread, m.ID),
			State: m.State, Body: clip(m.Body), Reply: clip(m.Reply), At: m.CreatedAt})
	}
	return out, nil
}

var gitObject = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

const fileAtMax = 1 << 20

// FileAt is a file as a turn left it: its content in the tree the turn's snapshot took, from a
// repository a turn of this node changed. The node's user only.
func (n *Node) FileAt(ctx context.Context, p Principal, repo, tree, file string) (string, error) {
	if !p.local() || (p.Kind != "user") {
		return "", errf("refused", "a file's versions are for the node's user")
	}
	clean := path.Clean(file)
	if !gitObject.MatchString(tree) || file == "" || clean != file || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", errf("invalid", "a tree id and a path inside the repository")
	}
	known, err := n.st.ChangedRepo(ctx, repo, tree)
	if err != nil {
		return "", errf("unavailable", "%v", err)
	}
	if !known {
		return "", errf("not_found", "no turn of this node left that tree in %s", repo)
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "git", "-C", repo, "cat-file", "blob", tree+":"+clean)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		return "", errf("not_found", "%s is not in that version (or the snapshot is gone)", file)
	}
	b := o.Bytes()
	if len(b) > fileAtMax {
		return "", errf("invalid", "the file is %d KiB, over %d KiB to show", len(b)>>10, fileAtMax>>10)
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", errf("unsupported", "%s is binary", file)
	}
	return string(b), nil
}

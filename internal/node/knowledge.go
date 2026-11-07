package node

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/k1dory/acpost/internal/store"
)

// Knowledge: the node user's notes, given to an agent at the start of each new session (in the
// first prompt, after acpost's own lines): the notes for everywhere, and those whose directory the
// session runs under.

func (n *Node) Knowledge(ctx context.Context, p Principal) ([]store.Note, error) {
	if err := userOnly(p, "knowledge"); err != nil {
		return nil, err
	}
	out, err := n.st.Knowledge(ctx)
	if err != nil {
		return nil, errf("unavailable", "%v", err)
	}
	return out, nil
}

func (n *Node) PutNote(ctx context.Context, p Principal, k store.Note) error {
	if err := userOnly(p, "knowledge"); err != nil {
		return err
	}
	if k.Scope = strings.TrimSpace(k.Scope); k.Scope != "" {
		dir, err := checkDir(k.Scope)
		if err != nil {
			return err
		}
		k.Scope = dir
	}
	if err := n.st.PutNote(ctx, k); err != nil {
		return errf("invalid", "%v", err)
	}
	return nil
}

func (n *Node) DeleteNote(ctx context.Context, p Principal, name string) error {
	if err := userOnly(p, "knowledge"); err != nil {
		return err
	}
	if err := n.st.DeleteNote(ctx, name); errors.Is(err, store.ErrNoNote) {
		return errf("not_found", "%v", err)
	} else if err != nil {
		return errf("unavailable", "%v", err)
	}
	return nil
}

// knowledgeSender: a message the user's notes may go with — sent here by this node's own user or
// one of its agents (not a token client, not another node, not through the hub), whose reply could
// otherwise carry them away.
func knowledgeSender(m store.Message) bool {
	from, err := ParseAddress(m.From)
	return err == nil && m.Origin == "" && from.Node == "" && (from.Kind == "user" || from.Kind == "agent")
}

// knowledgeFor is what a new session in cwd is given: the enabled notes for everywhere and for a
// directory cwd is in (both resolved: a link in or out of a scope goes where it points), up to
// store.MaxKnowledge; empty when none.
func (n *Node) knowledgeFor(ctx context.Context, cwd string) (string, error) {
	notes, err := n.st.Knowledge(ctx)
	if err != nil {
		return "", err
	}
	if len(notes) == 0 {
		return "", nil
	}
	real := ""
	if cwd != "" {
		real, _ = filepath.EvalSymlinks(cwd) // unresolved: only the notes for everywhere
	}
	var b strings.Builder
	for _, k := range notes {
		if !k.Enabled || !under(real, k.Scope) {
			continue
		}
		part := "## " + k.Name + "\n" + strings.TrimSpace(k.Body) + "\n\n"
		if b.Len()+len(part) > store.MaxKnowledge {
			break
		}
		b.WriteString(part)
	}
	if b.Len() == 0 {
		return "", nil
	}
	return "[acpost] What the user keeps as knowledge for this work (follow it unless the task says otherwise):\n\n" +
		b.String() + "[acpost] The task:\n\n", nil
}

// under: dir is scope or inside it, both resolved (an empty scope: everywhere; a scope that does
// not resolve, or no dir: no).
func under(dir, scope string) bool {
	if scope == "" {
		return true
	}
	if dir == "" {
		return false
	}
	rs, err := filepath.EvalSymlinks(scope)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rs, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

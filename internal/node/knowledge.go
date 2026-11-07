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

// knowledgeFor is what a new session in cwd is given: the enabled notes for everywhere and for a
// directory cwd is in, up to store.MaxKnowledge; empty when none.
func (n *Node) knowledgeFor(ctx context.Context, cwd string) string {
	notes, err := n.st.Knowledge(ctx)
	if err != nil || len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, k := range notes {
		if !k.Enabled || !under(cwd, k.Scope) {
			continue
		}
		part := "## " + k.Name + "\n" + strings.TrimSpace(k.Body) + "\n\n"
		if b.Len()+len(part) > store.MaxKnowledge {
			break
		}
		b.WriteString(part)
	}
	if b.Len() == 0 {
		return ""
	}
	return "[acpost] What the user keeps as knowledge for this work (follow it unless the task says otherwise):\n\n" +
		b.String() + "[acpost] The task:\n\n"
}

// under: dir is scope or inside it (an empty scope: everywhere).
func under(dir, scope string) bool {
	if scope == "" {
		return true
	}
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(scope, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

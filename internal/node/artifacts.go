package node

// The artifacts library: what agents handed back and what their turns made, across every chat, for
// the node's user. The store lists them; here each local file is looked at (is it still there,
// how big), and each chat is called what the user named it.

import (
	"context"
	"errors"
	"net/url"
	"os"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/plat"
	"github.com/k1dory/acpost/internal/store"
)

const (
	artifactsPage    = 60
	artifactsPageMax = 200
	artifactHeadMax  = 4 << 20 // the most of a file's content a head carries
)

// Artifacts lists a page of the node's user's artifacts, newest first.
func (n *Node) Artifacts(ctx context.Context, p Principal, q store.ArtifactQuery) (client.Artifacts, error) {
	if err := userOnly(p, "artifacts"); err != nil {
		return client.Artifacts{}, err
	}
	if q.Limit <= 0 {
		q.Limit = artifactsPage
	}
	q.Limit = min(q.Limit, artifactsPageMax)
	items, next, err := n.st.Artifacts(ctx, q)
	if errors.Is(err, store.ErrBadCursor) {
		return client.Artifacts{}, errf("invalid", "before: a cursor a page of artifacts gave")
	}
	if err != nil {
		return client.Artifacts{}, errf("unavailable", "%v", err)
	}
	titles, _ := n.st.ChatTitles(ctx)
	cwds := map[string]string{}
	for i := range items {
		a := &items[i]
		if t := titles["thread:"+a.Thread]; t != "" {
			a.Title = t
		}
		// The chat's directory: what a relative link in a reply's file is read from.
		if a.Thread != "" {
			a.Cwd = n.artifactCwd(ctx, a, cwds)
		}
		switch a.Kind {
		case "link":
			if u, err := url.Parse(a.URI); err == nil && u.Scheme == "file" {
				if local, ok := plat.LocalFile(u.Path); ok {
					a.Path = local
					lookAt(a)
				}
			}
		case "change":
			a.Type = linkType(a.Name, "")
			lookAt(a)
		}
	}
	if items == nil {
		items = []store.Artifact{}
	}
	return client.Artifacts{Items: items, Next: next}, nil
}

// artifactCwd is the directory of an artifact's chat: its own message's, else the thread's (its
// root's, or, the root collected by retention, any message of it still kept). cwds caches the
// threads'.
func (n *Node) artifactCwd(ctx context.Context, a *store.Artifact, cwds map[string]string) string {
	if m, err := n.st.Get(ctx, a.Msg); err == nil {
		if cwd := taskCwd(m); cwd != "" {
			return cwd
		}
	}
	if cwd, ok := cwds[a.Thread]; ok {
		return cwd
	}
	cwd := ""
	if root, err := n.st.Get(ctx, a.Thread); err == nil {
		cwd = taskCwd(root)
	}
	for after := ""; cwd == ""; {
		page, next, err := n.st.List(ctx, store.Filter{Thread: a.Thread, After: after, Limit: 100})
		if err != nil {
			break
		}
		for _, m := range page {
			if cwd = taskCwd(m); cwd != "" {
				break
			}
		}
		if next == "" || next == after {
			break
		}
		after = next
	}
	cwds[a.Thread] = cwd
	return cwd
}

// lookAt says whether an artifact's file is still there, and its size. Only its entry is read: a
// file the listing names is never opened.
func lookAt(a *store.Artifact) {
	st, err := os.Stat(a.Path)
	if err != nil || !st.Mode().IsRegular() {
		a.Gone = true
		return
	}
	a.Size = st.Size()
}

// ArtifactHead is a file of a message's reply with the start of its content (at most max bytes,
// at most artifactHeadMax): what the library shows of it.
func (n *Node) ArtifactHead(ctx context.Context, p Principal, msg, name string, max int) (store.File, error) {
	if err := userOnly(p, "artifacts"); err != nil {
		return store.File{}, err
	}
	if max <= 0 || max > artifactHeadMax {
		max = artifactHeadMax
	}
	f, err := n.st.ReplyFileHead(ctx, msg, name, max)
	if errors.Is(err, store.ErrNotFound) {
		return store.File{}, errf("not_found", "%s has no reply file %s", msg, name)
	}
	if err != nil {
		return store.File{}, errf("unavailable", "%v", err)
	}
	return f, nil
}

package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The tree under a message: what its agent asked, and what was asked in turn, in order.
func TestTree(t *testing.T) {
	n := newNode(t, inboxCfg+"  helper: {driver: inbox}\n")
	root, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "start", Task: "T"})
	tok := n.issueAgentToken("bot")
	n.setCurrent(tok, root.ID, "")
	p, _ := n.PrincipalForToken(tok)
	child, _, err := n.Send(ctx, p, SendRequest{To: "helper", Body: "sub"})
	if err != nil {
		t.Fatal(err)
	}
	tok2 := n.issueAgentToken("helper")
	n.setCurrent(tok2, child.ID, "")
	p2, _ := n.PrincipalForToken(tok2)
	grand, _, _ := n.Send(ctx, p2, SendRequest{To: "bot", Body: "subsub"})
	tree, err := n.Tree(ctx, me, root.ID)
	if err != nil || len(tree) != 2 || tree[0].ID != child.ID || tree[0].Depth != 1 || tree[1].ID != grand.ID || tree[1].Parent != child.ID || tree[0].Body != "sub" {
		t.Fatalf("%+v %v", tree, err)
	}
	if _, err := n.Tree(ctx, me, "msg_none"); err == nil {
		t.Fatal("a tree of nothing")
	}
}

// A file as a turn left it: from a tree a turn recorded, in that repository; the node's user only;
// no path out of the repository.
func TestFileAt(t *testing.T) {
	n := newNode(t, inboxCfg)
	dir := testRepo(t)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("version two\n"), 0o644)
	snap := snapshot(ctx, dir)
	if snap == nil {
		t.Fatal("no snapshot")
	}
	m, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	if err := n.st.AddChanges(ctx, store.TurnChanges{Msg: m.ID, Agent: "agent:bot", Repo: snap.repo, Before: snap.tree, After: snap.tree, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got, err := n.FileAt(ctx, me, snap.repo, snap.tree, "f.txt")
	if err != nil || got != "version two\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := n.FileAt(ctx, me, snap.repo, snap.tree, "../etc/passwd"); code(err) != "invalid" {
		t.Fatalf("out of the repository: %v", err)
	}
	if _, err := n.FileAt(ctx, me, t.TempDir(), snap.tree, "f.txt"); code(err) != "not_found" {
		t.Fatalf("another repository: %v", err)
	}
	if _, err := n.FileAt(ctx, Principal{Kind: "agent", Name: "bot"}, snap.repo, snap.tree, "f.txt"); code(err) != "refused" {
		t.Fatalf("an agent: %v", err)
	}
}

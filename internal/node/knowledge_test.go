package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// A new session's first prompt carries the notes for everywhere and for its directory, not the
// others; the next prompt of the session does not repeat them; only the node's user keeps notes.
func TestKnowledge(t *testing.T) {
	n := acpNode(t, `allow_from: ["user:*", "client:*"]`)
	in, out := t.TempDir(), t.TempDir()
	for _, k := range []store.Note{
		{Name: "style", Body: "Use tabs.", Enabled: true},
		{Name: "here", Body: "Run make check.", Scope: in, Enabled: true},
		{Name: "there", Body: "Never deploy.", Scope: out, Enabled: true},
		{Name: "off", Body: "Disabled note.", Enabled: false},
	} {
		if err := n.PutNote(ctx, me, k); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.PutNote(ctx, Principal{Kind: "agent", Name: "fake"}, store.Note{Name: "x", Body: "y", Enabled: true}); code(err) != "refused" {
		t.Fatalf("an agent kept a note: %v", err)
	}
	if err := n.PutNote(ctx, me, store.Note{Name: "x", Body: "y", Scope: "rel/dir"}); code(err) != "invalid" {
		t.Fatalf("a relative scope: %v", err)
	}
	m := ask(t, n, SendRequest{Body: "prompt", Cwd: in})
	for _, want := range []string{"Use tabs.", "Run make check.", "```mermaid"} {
		if !strings.Contains(m.Reply, want) {
			t.Fatalf("no %q in %q", want, m.Reply)
		}
	}
	for _, not := range []string{"Never deploy.", "Disabled note."} {
		if strings.Contains(m.Reply, not) {
			t.Fatalf("%q in %q", not, m.Reply)
		}
	}
	again := ask(t, n, SendRequest{Body: "prompt", Thread: m.Thread})
	if strings.Contains(again.Reply, "Use tabs.") || strings.Contains(again.Reply, "```mermaid") {
		t.Fatalf("the notes again in the same session: %q", again.Reply)
	}
	// A token client starts a session: no notes go with its message.
	ci := Principal{Kind: "client", Name: "ci", Token: &config.Token{Name: "ci", SendTo: []string{"agent:*"}}}
	cm, _, err := n.Send(ctx, ci, SendRequest{To: "fake", Body: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if w, err := n.Wait(ctx, ci, cm.ID, 30*time.Second); err != nil || strings.Contains(w.Reply, "Use tabs.") || strings.Contains(w.Reply, "```mermaid") {
		t.Fatalf("a client got the notes: %q %v", w.Reply, err)
	}
	// A link into a scope is in it; the scope's notes go with a session there.
	link := filepath.Join(t.TempDir(), "into-out")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	if lm := ask(t, n, SendRequest{Body: "prompt", Cwd: link}); !strings.Contains(lm.Reply, "Never deploy.") || strings.Contains(lm.Reply, "Run make check.") {
		t.Fatalf("through a link: %q", lm.Reply)
	}
	if err := n.DeleteNote(ctx, me, "style"); err != nil {
		t.Fatal(err)
	}
	if list, _ := n.Knowledge(ctx, me); len(list) != 3 {
		t.Fatalf("%+v", list)
	}
}

package node

import (
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// A new session's first prompt carries the notes for everywhere and for its directory, not the
// others; the next prompt of the session does not repeat them; only the node's user keeps notes.
func TestKnowledge(t *testing.T) {
	n := acpNode(t, "")
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
	for _, want := range []string{"Use tabs.", "Run make check."} {
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
	if strings.Contains(again.Reply, "Use tabs.") {
		t.Fatalf("the notes again in the same session: %q", again.Reply)
	}
	if err := n.DeleteNote(ctx, me, "style"); err != nil {
		t.Fatal(err)
	}
	if list, _ := n.Knowledge(ctx, me); len(list) != 3 {
		t.Fatalf("%+v", list)
	}
}

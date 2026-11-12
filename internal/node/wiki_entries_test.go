package node

import (
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// Every wiki's pages are the node user's only; a limit over the most (or none) is the most, and the
// cursor goes on where the part ended.
func TestWikiEntries(t *testing.T) {
	n := newNode(t, "node: {name: n}\nagents: {bot: {driver: inbox}}\n")
	if _, _, err := n.WikiEntries(ctx, Principal{Kind: "agent", Name: "bot"}, "", 0); code(err) != "refused" {
		t.Fatalf("an agent: %v", err)
	}
	if ok, err := n.st.StartWiki(ctx, "/r", "agent:a", "en", "m"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := n.st.FinishWiki(ctx, "/r", "m", []store.WikiPage{{Slug: "a", Title: "A", Body: "x"}, {Slug: "b", Title: "B", Body: "[[a]]"}}, ""); err != nil {
		t.Fatal(err)
	}
	part, next, err := n.WikiEntries(ctx, me, "", 1)
	if err != nil || len(part) != 1 || part[0].Slug != "a" || next == "" {
		t.Fatalf("%+v %q %v", part, next, err)
	}
	part, next, err = n.WikiEntries(ctx, me, next, wikiEntriesMax+5)
	if err != nil || len(part) != 1 || part[0].Body != "[[a]]" || next != "" {
		t.Fatalf("%+v %q %v", part, next, err)
	}
}

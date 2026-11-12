package node

import (
	"fmt"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// Review round 1: with more pages than a part holds, no limit and a limit over the most both give
// the most (wikiEntriesMax) and a cursor, and the rest follows from it.
func TestWikiEntriesClampsTheLimit(t *testing.T) {
	n := newNode(t, "node: {name: n}\nagents: {bot: {driver: inbox}}\n")
	const total = wikiEntriesMax + 20
	pages := make([]store.WikiPage, total)
	for i := range pages {
		pages[i] = store.WikiPage{Slug: fmt.Sprintf("p%04d", i), Title: "P", Body: "x"}
	}
	if ok, err := n.st.StartWiki(ctx, "/r", "agent:a", "en", "m"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := n.st.FinishWiki(ctx, "/r", "m", pages, ""); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, wikiEntriesMax * 10} {
		part, next, err := n.WikiEntries(ctx, me, "", limit)
		if err != nil || len(part) != wikiEntriesMax || next == "" {
			t.Fatalf("limit %d: %d pages, next %q, %v", limit, len(part), next, err)
		}
		rest, after, err := n.WikiEntries(ctx, me, next, limit)
		if err != nil || len(rest) != total-wikiEntriesMax || after != "" || rest[0].Slug != fmt.Sprintf("p%04d", wikiEntriesMax) {
			t.Fatalf("limit %d, the rest: %d pages, next %q, %v", limit, len(rest), after, err)
		}
	}
}

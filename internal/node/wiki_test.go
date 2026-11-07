package node

import (
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// An agent writes a directory's wiki as a task; its pages are taken in (a duplicate dropped, a
// parent that is no page cleared); only the node's user may; a wiki being written is not started
// twice.
func TestWiki(t *testing.T) {
	n := acpNode(t, "")
	dir := t.TempDir()
	if _, err := n.WriteWiki(ctx, Principal{Kind: "agent", Name: "fake"}, dir, "agent:fake", "en"); code(err) != "refused" {
		t.Fatalf("an agent: %v", err)
	}
	if _, err := n.WriteWiki(ctx, me, dir, "agent:nobody", "en"); code(err) != "invalid" {
		t.Fatalf("no such agent: %v", err)
	}
	m, err := n.WriteWiki(ctx, me, dir, "agent:fake", "ru")
	if err != nil {
		t.Fatal(err)
	}
	if m.Meta["wiki"] == "" || !strings.Contains(m.Body, "Russian") {
		t.Fatalf("%+v", m)
	}
	deadline := time.Now().Add(30 * time.Second)
	var w store.Wiki
	for {
		list, err := n.Wikis(ctx, me)
		if err != nil || len(list) != 1 {
			t.Fatalf("%+v %v", list, err)
		}
		if w = list[0]; w.Status != "writing" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still writing")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if w.Status != "ready" || w.Pages != 2 || w.Agent != "agent:fake" || w.Lang != "ru" {
		t.Fatalf("%+v", w)
	}
	pages, _ := n.WikiPages(ctx, me, w.Repo)
	if len(pages) != 2 || pages[0].Slug != "overview" || pages[0].Title != "Overview" || pages[1].Parent != "" || !strings.Contains(pages[1].Body, "mermaid") {
		t.Fatalf("%+v", pages)
	}
	if err := n.DeleteWiki(ctx, me, w.Repo); err != nil {
		t.Fatal(err)
	}
	if list, _ := n.Wikis(ctx, me); len(list) != 0 {
		t.Fatalf("%+v", list)
	}
}

func TestParseWiki(t *testing.T) {
	got := parseWiki("x\n<<<PAGE slug=\"a\" title=\"A\">>>\none\n<<<END>>>\n<<<PAGE slug=\"b\" title=\"B\" parent=\"a\">>>\ntwo\n<<<END>>>\n<<<PAGE slug=\"Bad Slug\" title=\"C\">>>\nno\n<<<END>>>")
	if len(got) != 2 || got[1].Parent != "a" || got[0].Body != "one" {
		t.Fatalf("%+v", got)
	}
}

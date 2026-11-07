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
	// An agent with no read-only mode writes no wiki.
	if _, err := n.WriteWiki(ctx, me, dir, "agent:fake", "en"); code(err) != "unsupported" {
		t.Fatalf("no read-only mode: %v", err)
	}
	t.Setenv("FAKE_MODES", "1")
	n = acpNode(t, "")
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
	if m.Meta["wiki"] == "" || m.Meta["acp_mode"] != "plan" || m.Meta["acp_mode_strict"] != "1" || !strings.Contains(m.Body, "Russian") {
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

// A read-only mode that cannot be set: the turn does not run, the wiki fails.
func TestWikiModeNotSet(t *testing.T) {
	t.Setenv("FAKE_MODES", "broken")
	n := acpNode(t, "")
	if _, err := n.WriteWiki(ctx, me, t.TempDir(), "agent:fake", "en"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		list, _ := n.Wikis(ctx, me)
		if len(list) == 1 && list[0].Status != "writing" {
			if list[0].Status != "failed" || list[0].Pages != 0 {
				t.Fatalf("%+v", list[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %+v", list)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestParseWiki(t *testing.T) {
	got := parseWiki("x\n<<<PAGE slug=\"a\" title=\"A\">>>\none\n<<<END>>>\n<<<PAGE slug=\"b\" title=\"B\" parent=\"a\">>>\ntwo\n<<<END>>>\n<<<PAGE slug=\"Bad Slug\" title=\"C\">>>\nno\n<<<END>>>")
	if len(got) != 2 || got[1].Parent != "a" || got[0].Body != "one" {
		t.Fatalf("%+v", got)
	}
	// Three levels and a cycle: two levels, every page reachable.
	got = parseWiki("<<<PAGE slug=\"a\" title=\"A\">>>\n1\n<<<END>>>\n<<<PAGE slug=\"b\" title=\"B\" parent=\"a\">>>\n2\n<<<END>>>\n<<<PAGE slug=\"c\" title=\"C\" parent=\"b\">>>\n3\n<<<END>>>\n" +
		"<<<PAGE slug=\"x\" title=\"X\" parent=\"y\">>>\n4\n<<<END>>>\n<<<PAGE slug=\"y\" title=\"Y\" parent=\"x\">>>\n5\n<<<END>>>")
	for _, p := range got {
		if p.Parent != "" {
			for _, q := range got {
				if q.Slug == p.Parent && q.Parent != "" {
					t.Fatalf("three levels: %+v", got)
				}
			}
		}
	}
	if got[2].Parent != "" { // c's parent b has a parent: c goes to the top
		t.Fatalf("%+v", got)
	}
}

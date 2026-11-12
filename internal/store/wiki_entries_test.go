package store

import (
	"context"
	"strings"
	"testing"
)

// Every wiki's pages come in parts: by repository and slug, a part ends at its count or once its
// text reaches the size (a page larger than that alone still comes), and the cursor goes on from
// the last page without repeating or skipping one. A wiki being written for the first time has none.
func TestWikiEntriesPages(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	put := func(repo string, pages ...WikiPage) {
		if ok, err := s.StartWiki(ctx, repo, "agent:a", "en", "m-"+repo); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if err := s.FinishWiki(ctx, repo, "m-"+repo, pages, ""); err != nil {
			t.Fatal(err)
		}
	}
	put("/b", WikiPage{Slug: "z", Title: "Z", Body: "zz"}, WikiPage{Slug: "a", Title: "A", Parent: "z", Body: strings.Repeat("x", 100)})
	put("/a", WikiPage{Slug: "overview", Title: "Overview", Body: "see [[a]]"})
	if ok, _ := s.StartWiki(ctx, "/c", "agent:a", "en", "m-c"); !ok {
		t.Fatal("start /c")
	}

	all, next, err := s.WikiEntries(ctx, "", 10, 1<<20)
	if err != nil || next != "" || len(all) != 3 {
		t.Fatalf("%+v %q %v", all, next, err)
	}
	if all[0].Repo != "/a" || all[1].Slug != "a" || all[1].Parent != "z" || all[2].Slug != "z" || all[0].Updated.IsZero() || all[0].Body != "see [[a]]" {
		t.Fatalf("%+v", all)
	}

	var got []string
	after := ""
	for i := 0; ; i++ {
		part, next, err := s.WikiEntries(ctx, after, 1, 1<<20)
		if err != nil || len(part) != 1 || i > 3 {
			t.Fatalf("%d: %+v %v", i, part, err)
		}
		got = append(got, part[0].Repo+" "+part[0].Slug)
		if next == "" {
			break
		}
		after = next
	}
	if strings.Join(got, ",") != "/a overview,/b a,/b z" {
		t.Fatal(got)
	}

	// By size: the large page alone in its part.
	part, next, _ := s.WikiEntries(ctx, "/a\x00overview", 10, 50)
	if len(part) != 1 || part[0].Slug != "a" || next == "" {
		t.Fatalf("%+v %q", part, next)
	}
	part, next, _ = s.WikiEntries(ctx, next, 10, 50)
	if len(part) != 1 || part[0].Slug != "z" || next != "" {
		t.Fatalf("%+v %q", part, next)
	}
}

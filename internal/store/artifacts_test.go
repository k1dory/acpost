package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A store with a finished reply carrying two files, and two turns that changed files, the later
// one changing a document the earlier one made.
func artifactStore(t *testing.T) (s *Store, replied, changed, later string) {
	s = openTest(t, Options{})
	m := msg("agent:a", "write the report\nplease")
	mustInsert(t, s, m)
	c, _, _ := s.Claim(ctx, "agent:a", time.Minute)
	files := []File{
		{Name: "chart.png", Type: "image/png", Data: []byte("\x89PNG not really")},
		{Name: "notes.md", Type: "text/markdown", URI: "file:///tmp/notes.md"},
	}
	if err := s.AttachReply(ctx, m.ID, c.LeaseID, files, lim); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, m.ID, c.LeaseID, Done, "done", "", ""); err != nil {
		t.Fatal(err)
	}
	m2 := msg("agent:b", "build the site")
	mustInsert(t, s, m2)
	m3 := msg("agent:b", "fix the report")
	mustInsert(t, s, m3)
	now := time.Now()
	add := func(id string, at time.Time, files ...ChangedFile) {
		t.Helper()
		if err := s.AddChanges(ctx, TurnChanges{Msg: id, Agent: "agent:b", Start: at, At: at, Repo: "/work/site", Files: files}); err != nil {
			t.Fatal(err)
		}
	}
	add(m2.ID, now.Add(time.Hour),
		ChangedFile{Path: "docs/report.md", Status: "A"},
		ChangedFile{Path: "main.go", Status: "M"},
		ChangedFile{Path: "old.html", Status: "D"},
		ChangedFile{Path: ".github/notes.md", Status: "A"},
		ChangedFile{Path: "web/node_modules/x/README.md", Status: "A"},
		ChangedFile{Path: "out/data.json", Status: "A"},
		ChangedFile{Path: "package.json", Status: "M"},
		ChangedFile{Path: "/elsewhere/page.HTML", Status: "A"},
	)
	add(m3.ID, now.Add(2*time.Hour), ChangedFile{Path: "docs/report.md", Status: "M"})
	return s, m.ID, m2.ID, m3.ID
}

func names(as []Artifact) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind+":"+a.Name)
	}
	return strings.Join(out, " ")
}

func TestArtifactsListDocumentsNewestFirst(t *testing.T) {
	s, replied, changed, later := artifactStore(t)
	got, next, err := s.Artifacts(ctx, ArtifactQuery{})
	if err != nil || next != "" {
		t.Fatal(next, err)
	}
	// The report once, at its latest change; sources, deletions, hidden folders, node_modules and an
	// edited JSON file are not documents.
	if n := names(got); n != "change:report.md change:page.HTML change:data.json link:notes.md file:chart.png" {
		t.Fatalf("listed %s", n)
	}
	r := got[0]
	if r.Msg != later || r.Thread != later || r.Agent != "agent:b" || r.Status != "M" || r.Size != -1 ||
		r.Path != filepath.FromSlash("/work/site/docs/report.md") || r.Title != "fix the report" {
		t.Fatalf("report %+v", r)
	}
	if got[1].Path != filepath.FromSlash("/elsewhere/page.HTML") || got[1].Msg != changed {
		t.Fatalf("a file outside the repository: %+v", got[1])
	}
	f := got[4]
	if f.Msg != replied || f.Type != "image/png" || f.Size != 15 || f.Title != "write the report" || f.Agent != "agent:a" || f.At.IsZero() {
		t.Fatalf("reply file %+v", f)
	}
	if got[3].URI != "file:///tmp/notes.md" {
		t.Fatalf("link %+v", got[3])
	}
}

func TestArtifactsPageWithoutRepeats(t *testing.T) {
	s, _, _, _ := artifactStore(t)
	var all []Artifact
	before := ""
	for range 10 {
		got, next, err := s.Artifacts(ctx, ArtifactQuery{Before: before, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
		if next == "" {
			break
		}
		before = next
	}
	if n := names(all); n != "change:report.md change:page.HTML change:data.json link:notes.md file:chart.png" {
		t.Fatalf("paged %s", n)
	}
	if _, _, err := s.Artifacts(ctx, ArtifactQuery{Before: "1.2.3"}); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestArtifactsMatch(t *testing.T) {
	s, _, _, _ := artifactStore(t)
	for q, want := range map[string]string{
		"CHART":     "file:chart.png",
		"/tmp/":     "link:notes.md",
		"work/site": "change:report.md change:data.json",
		"50%":       "",
		"_":         "",
	} {
		got, _, err := s.Artifacts(ctx, ArtifactQuery{Match: q})
		if err != nil || names(got) != want {
			t.Errorf("%q: %s %v", q, names(got), err)
		}
	}
}

func TestReplyFileHead(t *testing.T) {
	s, replied, _, _ := artifactStore(t)
	f, err := s.ReplyFileHead(ctx, replied, "chart.png", 4)
	if err != nil || string(f.Data) != "\x89PNG" || f.Size != 15 || f.Type != "image/png" {
		t.Fatalf("%+v %v", f, err)
	}
	if f, err := s.ReplyFileHead(ctx, replied, "notes.md", 4); err != nil || f.Data != nil || f.URI == "" {
		t.Fatalf("a link: %+v %v", f, err)
	}
	if _, err := s.ReplyFileHead(ctx, replied, "nope", 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

package store

import (
	"fmt"
	"testing"
	"time"
)

// A turn that made thousands of source files (review round 6): the listing reads the history
// once, in one pass, and stays quick and right. The recursive plan it replaced took seconds here.
func TestArtifactsScaleWithManyChangedSources(t *testing.T) {
	s := openTest(t, Options{})
	now := time.Now()
	turn := func(at time.Duration, files []ChangedFile) {
		t.Helper()
		m := msg("agent:b", "turn")
		mustInsert(t, s, m)
		ts := now.Add(at)
		if err := s.AddChanges(ctx, TurnChanges{Msg: m.ID, Agent: "agent:b", Start: ts, At: ts, Repo: "/w", Files: files}); err != nil {
			t.Fatal(err)
		}
	}
	var made, edited []ChangedFile
	for i := range 10000 {
		made = append(made, ChangedFile{Path: fmt.Sprintf("pkg%d/file%d.go", i%50, i), Status: "A"})
	}
	made = append(made, ChangedFile{Path: "docs/guide.md", Status: "A"}, ChangedFile{Path: "out/report.json", Status: "A"}, ChangedFile{Path: "site/index.html", Status: "A"})
	turn(time.Minute, made)
	for i := range 5000 {
		edited = append(edited, ChangedFile{Path: fmt.Sprintf("pkg%d/file%d.go", i%50, i), Status: "M"})
	}
	edited = append(edited, ChangedFile{Path: "out/final.json", From: "out/report.json", Status: "R"})
	turn(2*time.Minute, edited)

	start := time.Now()
	var all []Artifact
	for before := ""; ; {
		got, next, err := s.Artifacts(ctx, ArtifactQuery{Before: before, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
		if next == "" {
			break
		}
		before = next
	}
	took := time.Since(start)
	if n := names(all); n != "change:final.json change:index.html change:guide.md" {
		t.Fatalf("listed %s: the renamed report is not at its old path", n)
	}
	if took > 2*slow*time.Second { // CI runs -race: slow is 20 there
		t.Fatalf("four pages took %v", took)
	}
	t.Logf("four pages of one over %d changed files: %v", len(made)+len(edited), took)
}

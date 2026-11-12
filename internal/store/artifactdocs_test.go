package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Review round 7: the documents on disk are a projection kept with the changes, built once for an
// older store, kept through retention; one rule of case for every artifact.

func addTurn(t *testing.T, s *Store, at time.Time, files ...ChangedFile) string {
	t.Helper()
	m := msg("agent:b", "turn")
	mustInsert(t, s, m)
	if err := s.AddChanges(ctx, TurnChanges{Msg: m.ID, Agent: "agent:b", Start: at, At: at, Repo: "/w", Files: files}); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func listAll(t *testing.T, s *Store, q ArtifactQuery) []Artifact {
	t.Helper()
	var all []Artifact
	for before := ""; ; {
		q.Before = before
		got, next, err := s.Artifacts(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
		if next == "" {
			return all
		}
		before = next
	}
}

func TestArtifactDocsBuiltForAnOlderStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s", "store.db")
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	addTurn(t, s, now.Add(1*time.Minute), ChangedFile{Path: "report.json", Status: "A"}, ChangedFile{Path: "a.md", Status: "A"}, ChangedFile{Path: "x.go", Status: "A"})
	addTurn(t, s, now.Add(2*time.Minute), ChangedFile{Path: "final.json", From: "report.json", Status: "R"})
	addTurn(t, s, now.Add(3*time.Minute), ChangedFile{Path: "b.md", Status: "R"}) // an old rename row: no source
	addTurn(t, s, now.Add(4*time.Minute), ChangedFile{Path: "a.md", Status: "D"})
	want := names(listAll(t, s, ArtifactQuery{Limit: 2}))
	// A store from before the projection: the changes, no artifact_docs.
	if _, err := s.w.Exec("DROP TABLE artifact_docs; DROP TABLE artifact_docs_built"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := names(listAll(t, s, ArtifactQuery{Limit: 2})); got != want || got != "change:b.md change:final.json" {
		t.Fatalf("rebuilt: %s, kept: %s", got, want)
	}
}

func TestArtifactDocsThroughRetention(t *testing.T) {
	s := openTest(t, Options{})
	old := time.Now().Add(-time.Hour)
	finish := func(id string) {
		t.Helper()
		c, _, _ := s.Claim(ctx, "agent:b", time.Minute)
		if c.ID != id {
			t.Fatalf("claimed %s, not %s", c.ID, id)
		}
		if err := s.Complete(ctx, id, c.LeaseID, Done, "ok", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	made := addTurn(t, s, old, ChangedFile{Path: "report.json", Status: "A"}, ChangedFile{Path: "gone.md", Status: "A"})
	finish(made)
	time.Sleep(5 * time.Millisecond)
	cut := time.Now()
	time.Sleep(5 * time.Millisecond)
	edited := addTurn(t, s, old.Add(time.Minute), ChangedFile{Path: "report.json", Status: "M"}) // still running: kept
	if n, err := s.GC(ctx, cut); err != nil || n != 1 {
		t.Fatalf("collected %d %v", n, err)
	}
	// The JSON's creation is collected, its latest edit is not: still made, still listed. The
	// document whose latest change was collected goes with it.
	got := listAll(t, s, ArtifactQuery{})
	if names(got) != "change:report.json" || got[0].Msg != edited {
		t.Fatalf("listed %s %+v", names(got), got)
	}
	finish(edited)
	if _, err := s.GC(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := listAll(t, s, ArtifactQuery{}); len(got) != 0 {
		t.Fatalf("after its latest change went: %s", names(got))
	}
}

func TestArtifactsMatchFoldsCaseOneWay(t *testing.T) {
	s := openTest(t, Options{})
	m := msg("agent:a", "x")
	mustInsert(t, s, m)
	c, _, _ := s.Claim(ctx, "agent:a", time.Minute)
	files := []File{{Name: "Отчёт.md", Type: "text/markdown", Data: []byte("1")}, {Name: "ОТЧЁТ-2.md", Type: "text/markdown", Data: []byte("2")}, {Name: "other.md", Type: "text/markdown", Data: []byte("3")}}
	if err := s.AttachReply(ctx, m.ID, c.LeaseID, files, lim); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Hour)
	addTurn(t, s, now, ChangedFile{Path: "docs/Отчёт-3.md", Status: "A"}, ChangedFile{Path: "docs/план.md", Status: "A"})
	addTurn(t, s, now.Add(time.Minute), ChangedFile{Path: "ОТЧЁТ-4.md", Status: "A"})
	for _, q := range []string{"отчёт", "ОТЧЁТ", "Отчёт"} {
		got := listAll(t, s, ArtifactQuery{Match: q, Limit: 1})
		if n := names(got); n != "change:ОТЧЁТ-4.md change:Отчёт-3.md file:ОТЧЁТ-2.md file:Отчёт.md" {
			t.Errorf("%q, a page of one: %s", q, n)
		}
	}
}

// Many turns of many source files: a page is an indexed read of the projection, whatever the
// history holds, again and again.
func TestArtifactsManyTurnsStayQuick(t *testing.T) {
	s := openTest(t, Options{})
	now := time.Now()
	for i := range 400 {
		var files []ChangedFile
		for j := range 50 {
			files = append(files, ChangedFile{Path: fmt.Sprintf("pkg%d/f%d.go", j, i), Status: "A"})
		}
		if i%40 == 0 {
			files = append(files, ChangedFile{Path: fmt.Sprintf("docs/r%03d.md", i), Status: "A"})
		}
		addTurn(t, s, now.Add(time.Duration(i)*time.Second), files...)
	}
	addTurn(t, s, now.Add(time.Hour), ChangedFile{Path: "out/report.json", Status: "A"})
	addTurn(t, s, now.Add(2*time.Hour), ChangedFile{Path: "out/final.json", From: "out/report.json", Status: "R"})

	// The listing reads the projection by its index, never the changes.
	plan := ""
	rows, err := s.r.Query("EXPLAIN QUERY PLAN "+docsSQL, 1, 1, 1, 1, 1, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, b, c int
		var d string
		rows.Scan(&a, &b, &c, &d)
		plan += d + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "artifact_docs_listed") || strings.Contains(plan, "turn_changes") {
		t.Fatalf("plan:\n%s", plan)
	}

	start := time.Now()
	for range 20 {
		if got, _, err := s.Artifacts(ctx, ArtifactQuery{Limit: 3}); err != nil || names(got) != "change:final.json change:r360.md change:r320.md" {
			t.Fatalf("%s %v", names(got), err)
		}
	}
	all := listAll(t, s, ArtifactQuery{Limit: 4})
	if len(all) != 11 {
		t.Fatalf("paged %d: %s", len(all), names(all))
	}
	if took := time.Since(start); took > 2*slow*time.Second {
		t.Fatalf("twenty pages and a paging took %v", took)
	}
}

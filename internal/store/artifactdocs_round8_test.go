package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Review round 8: a build cut short, turns recorded out of order, and the plans of the queries.

func planOf(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.r.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var a, b, c int
		var d string
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		plan += d + "\n"
	}
	return plan
}

func TestArtifactDocsBuildCutShortIsMadeAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s", "store.db")
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := range 5 {
		addTurn(t, s, now.Add(time.Duration(i)*time.Minute), ChangedFile{Path: fmt.Sprintf("r%d.md", i), Status: "A"})
	}
	addTurn(t, s, now.Add(time.Hour), ChangedFile{Path: "r0.md", Status: "D"})
	want := names(listAll(t, s, ArtifactQuery{}))
	if _, err := s.w.Exec("DROP TABLE artifact_docs; DROP TABLE artifact_docs_built"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	defer func(n int) { docsBatch, afterDocsBatch = n, nil }(docsBatch)
	docsBatch = 2
	batches := 0
	afterDocsBatch = func() error { batches++; return errors.New("cut short") }
	if s, err := Open("sqlite://"+path, Options{}); err == nil {
		s.Close()
		t.Fatal("a build cut short opened")
	}
	if batches != 1 {
		t.Fatalf("%d batches before the cut", batches)
	}
	// The next open builds it all again; the one after does nothing.
	batches = 0
	afterDocsBatch = func() error { batches++; return nil }
	s, err = Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(listAll(t, s, ArtifactQuery{})); got != want || strings.Contains(got, "r0.md") || batches == 0 {
		t.Fatalf("rebuilt %s (%d batches), want %s", got, batches, want)
	}
	s.Close()
	batches = 0
	s, err = Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if batches != 0 || names(listAll(t, s, ArtifactQuery{})) != want {
		t.Fatalf("built again: %d batches", batches)
	}
}

// Every arrival order of the same turns gives the rows the order of their times gives.
func TestArtifactDocsAnyArrivalOrder(t *testing.T) {
	base := time.Now()
	turns := []struct {
		at    time.Duration
		files []ChangedFile
	}{
		{1, []ChangedFile{{Path: "report.json", Status: "A"}, {Path: "gone.md", Status: "A"}}},
		{2, []ChangedFile{{Path: "report.json", Status: "M"}, {Path: "config.json", Status: "M"}}},
		{3, []ChangedFile{{Path: "final.json", From: "report.json", Status: "R"}, {Path: "backup.json", From: "config.json", Status: "R"}}},
		{4, []ChangedFile{{Path: "config.json", Status: "A"}, {Path: "gone.md", Status: "D"}}},
		{5, []ChangedFile{{Path: "final.json", Status: "M"}, {Path: "report.json", Status: "A"}}},
	}
	rows := func(order []int) string {
		s := openTest(t, Options{})
		for _, i := range order {
			m := msg("agent:b", fmt.Sprintf("turn %d", i))
			mustInsert(t, s, m)
			at := base.Add(turns[i].at * time.Minute)
			if err := s.AddChanges(ctx, TurnChanges{Msg: m.ID, Agent: "agent:b", Start: at, At: at, Repo: "/w", Files: turns[i].files}); err != nil {
				t.Fatal(err)
			}
		}
		var out []string
		r, err := s.r.Query("SELECT path, ts, idx, status, made, listed, (SELECT substr(body, 1, 10) FROM contents c JOIN messages m ON m.seq = c.seq WHERE m.id = d.msg) FROM artifact_docs d ORDER BY path")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for r.Next() {
			var p, st, body string
			var ts, idx int64
			var made, listed bool
			if err := r.Scan(&p, &ts, &idx, &st, &made, &listed, &body); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(p, ts, idx, st, made, listed, body))
		}
		return strings.Join(out, "\n")
	}
	want := rows([]int{0, 1, 2, 3, 4})
	if !strings.Contains(want, "/w/final.json") || !strings.Contains(want, "/w/final.json1") || !strings.Contains(want, "0Mtrue trueturn 4") {
		t.Fatalf("in order:\n%s", want)
	}
	var permute func(p []int, k int)
	n := 0
	permute = func(p []int, k int) {
		if k == len(p) {
			n++
			if got := rows(p); got != want {
				t.Fatalf("order %v:\n%s\nwant\n%s", p, got, want)
			}
			return
		}
		for i := k; i < len(p); i++ {
			p[k], p[i] = p[i], p[k]
			permute(p, k+1)
			p[k], p[i] = p[i], p[k]
		}
	}
	permute([]int{0, 1, 2, 3, 4}, 0)
	if n != 120 {
		t.Fatal(n)
	}
}

func TestArtifactQueriesAreIndexSearches(t *testing.T) {
	s := openTest(t, Options{})
	for range 200 {
		m := msg("agent:a", "x")
		mustInsert(t, s, m)
	}
	for range 50 {
		m := msg("agent:a", "x")
		mustInsert(t, s, m)
		c, _, _ := s.Claim(ctx, "agent:a", time.Minute)
		if err := s.AttachReply(ctx, c.ID, c.LeaseID, []File{{Name: "a.png", Data: []byte("1")}, {Name: "b.md", Data: []byte("2")}, {Name: "c.pdf", Data: []byte("3")}}, lim); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.Exec("ANALYZE"); err != nil {
		t.Fatal(err)
	}
	batch := planOf(t, s, docsBatchSQL, 0, 0, 10)
	if !strings.Contains(batch, "SEARCH turn_changes USING INDEX turn_changes_ts") || strings.Contains(batch, "TEMP B-TREE") {
		t.Errorf("the build's batch:\n%s", batch)
	}
	for _, match := range []string{"", "md"} {
		files := planOf(t, s, replyFilesSQL, 1<<62, 1<<62, 1, 1<<62, 1<<62, 1<<62, match, match, match, 10)
		if strings.Contains(files, "TEMP B-TREE") || strings.Contains(files, "SCAN f") || !strings.Contains(files, "messages_reply_ts") {
			t.Errorf("reply files (match %q):\n%s", match, files)
		}
	}
}

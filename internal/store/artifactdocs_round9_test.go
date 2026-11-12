package store

import (
	"testing"
	"time"
)

// Review round 9: a rebuild keeps what the rows held of a history retention collected.
func TestArtifactDocsRebuildKeepsCollectedCreations(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		s := openTest(t, Options{})
		base := time.Now().Add(-time.Hour)
		turn := func(at time.Duration, files ...ChangedFile) string {
			t.Helper()
			m := msg("agent:b", "turn")
			mustInsert(t, s, m)
			ts := base.Add(at * time.Minute)
			if err := s.AddChanges(ctx, TurnChanges{Msg: m.ID, Agent: "agent:b", Start: ts, At: ts, Repo: "/w", Files: files}); err != nil {
				t.Fatal(err)
			}
			return m.ID
		}
		made := turn(1, ChangedFile{Path: "report.json", Status: "A"})
		c, _, _ := s.Claim(ctx, "agent:b", time.Minute)
		if c.ID != made {
			t.Fatal("claimed", c.ID)
		}
		if err := s.Complete(ctx, made, c.LeaseID, Done, "ok", "", ""); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		cut := time.Now()
		time.Sleep(5 * time.Millisecond)
		want := "report.json"
		if renamed {
			turn(3, ChangedFile{Path: "final.json", From: "report.json", Status: "R"}) // still running: kept
			want = "final.json"
		} else {
			turn(3, ChangedFile{Path: "report.json", Status: "M"})
		}
		if n, err := s.GC(ctx, cut); err != nil || n != 1 {
			t.Fatalf("collected %d %v", n, err)
		}
		// A turn recorded behind the later one, touching the path: its paths are rebuilt.
		turn(2, ChangedFile{Path: "report.json", Status: "M"}, ChangedFile{Path: "notes.md", Status: "A"})

		got := listAll(t, s, ArtifactQuery{})
		if names(got) != "change:"+want+" change:notes.md" {
			t.Fatalf("renamed %v: listed %s", renamed, names(got))
		}
		var mk bool
		if err := s.r.QueryRow("SELECT made FROM artifact_docs WHERE path = ?", "/w/"+want).Scan(&mk); err != nil || !mk {
			t.Fatalf("renamed %v: %s made %v %v", renamed, want, mk, err)
		}
	}
}

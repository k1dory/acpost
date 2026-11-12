package store

import (
	"testing"
	"time"
)

// A rename (review round 4): the source path is gone from then on, and what a turn created stays
// created under its new name, through a chain of renames; a row recorded before renames had their
// source keeps today's reading.
func TestArtifactsFollowRenames(t *testing.T) {
	s := openTest(t, Options{})
	now := time.Now()
	turn := func(body string, at time.Duration, files ...ChangedFile) string {
		t.Helper()
		m := msg("agent:b", body)
		mustInsert(t, s, m)
		ts := now.Add(at)
		if err := s.AddChanges(ctx, TurnChanges{Msg: m.ID, Agent: "agent:b", Start: ts, At: ts, Repo: "/w", Files: files}); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	turn("make", time.Minute, ChangedFile{Path: "report.json", Status: "A"}, ChangedFile{Path: "draft.md", Status: "A"}, ChangedFile{Path: "keep.json", Status: "A"})
	turn("rename", 2*time.Minute, ChangedFile{Path: "final.json", From: "report.json", Status: "R"}, ChangedFile{Path: "final.md", From: "draft.md", Status: "R"},
		ChangedFile{Path: "mid.json", From: "keep.json", Status: "R"})
	turn("rename again", 3*time.Minute, ChangedFile{Path: "last.json", From: "mid.json", Status: "R"})
	edited := turn("edit", 4*time.Minute, ChangedFile{Path: "final.json", Status: "M"})
	// Recorded before a rename said its source: as before, the source stays, the JSON not made.
	turn("old row", 5*time.Minute, ChangedFile{Path: "old-dest.json", Status: "R"})

	got, _, err := s.Artifacts(ctx, ArtifactQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if n := names(got); n != "change:final.json change:last.json change:final.md" {
		t.Fatalf("listed %s", n)
	}
	if got[0].Msg != edited || got[0].Status != "M" {
		t.Fatalf("a renamed JSON file edited: %+v", got[0])
	}
}

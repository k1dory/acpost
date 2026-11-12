package store

import (
	"testing"
	"time"
)

// A path used again (review round 5): what a turn made is carried by a rename only forward in
// time, from a file that was there then.
func TestArtifactsRenamesCarryOnlyEarlierCreations(t *testing.T) {
	s := openTest(t, Options{})
	now := time.Now()
	turn := func(at time.Duration, files ...ChangedFile) {
		t.Helper()
		m := msg("agent:b", "turn")
		mustInsert(t, s, m)
		ts := now.Add(at)
		if err := s.AddChanges(ctx, TurnChanges{Msg: m.ID, Agent: "agent:b", Start: ts, At: ts, Repo: "/w", Files: files}); err != nil {
			t.Fatal(err)
		}
	}
	// A config.json the user had is backed up; a later turn writes a new config.json.
	turn(1*time.Minute, ChangedFile{Path: "backup.json", From: "config.json", Status: "R"})
	turn(2*time.Minute, ChangedFile{Path: "config.json", Status: "A"})
	// A report made, renamed, and made again at its old path: two files, each with its own history.
	turn(3*time.Minute, ChangedFile{Path: "report.json", Status: "A"})
	turn(4*time.Minute, ChangedFile{Path: "final.json", From: "report.json", Status: "R"})
	turn(5*time.Minute, ChangedFile{Path: "report.json", Status: "A"})
	// Made, deleted, then a file the user put back there renamed: not the turn's.
	turn(6*time.Minute, ChangedFile{Path: "tmp.json", Status: "A"})
	turn(7*time.Minute, ChangedFile{Path: "tmp.json", Status: "D"})
	turn(8*time.Minute, ChangedFile{Path: "kept.json", From: "tmp.json", Status: "R"})

	got, _, err := s.Artifacts(ctx, ArtifactQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if n := names(got); n != "change:report.json change:final.json change:config.json" {
		t.Fatalf("listed %s", n)
	}
	if !got[0].At.After(got[1].At) {
		t.Fatalf("report.json at its own creation, after final.json's rename: %v %v", got[0].At, got[1].At)
	}
}

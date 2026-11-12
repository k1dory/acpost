package store

import (
	"testing"
	"time"
)

// A path is listed as its latest change left it (review round 1): ranked over its whole history
// before the rules are applied, not after.
func TestArtifactsFollowAPathsLatestChange(t *testing.T) {
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
	turn("make", time.Minute, ChangedFile{Path: "gone.md", Status: "A"}, ChangedFile{Path: "made.json", Status: "A"})
	turn("edit", 2*time.Minute, ChangedFile{Path: "gone.md", Status: "M"}, ChangedFile{Path: "config.json", Status: "M"})
	edited := turn("edit again", 3*time.Minute, ChangedFile{Path: "made.json", Status: "M"})
	turn("remove", 4*time.Minute, ChangedFile{Path: "gone.md", Status: "D"})

	got, _, err := s.Artifacts(ctx, ArtifactQuery{})
	if err != nil {
		t.Fatal(err)
	}
	// Created, edited, deleted: not there. A JSON file a turn created, then edited: at its edit, in
	// that turn's chat. A JSON file no turn created (configuration): not a document.
	if n := names(got); n != "change:made.json" {
		t.Fatalf("listed %s", n)
	}
	if a := got[0]; a.Msg != edited || a.Status != "M" || a.Title != "edit again" || !a.At.Equal(now.Add(3*time.Minute).Truncate(time.Millisecond)) {
		t.Fatalf("the JSON file at %+v", a)
	}
}

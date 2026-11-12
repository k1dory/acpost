package node

import (
	"os"
	"path/filepath"
	"testing"
)

// A rename says where the file came from (review round 4): the library retires that path.
func TestSnapshotDiffKeepsARenamesSource(t *testing.T) {
	dir := testRepo(t)
	os.WriteFile(filepath.Join(dir, "report.json"), []byte(`{"a": "a long enough body for git to see a rename"}`+"\n"), 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "report")
	before := snapshot(ctx, dir)
	if err := os.Rename(filepath.Join(dir, "report.json"), filepath.Join(dir, "final.json")); err != nil {
		t.Fatal(err)
	}
	files, _, _, err := diffSnaps(ctx, before, snapshot(ctx, dir))
	if err != nil || len(files) != 1 || files[0].Status != "R" || files[0].Path != "final.json" || files[0].From != "report.json" {
		t.Fatalf("%+v %v", files, err)
	}
	if got := parseNameStatus([]byte("M\x00a.md\x00R100\x00old.md\x00new.md\x00")); len(got) != 2 || got[0].From != "" || got[1].From != "old.md" || got[1].Path != "new.md" {
		t.Fatalf("%+v", got)
	}
}

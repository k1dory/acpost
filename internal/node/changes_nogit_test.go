package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpost/internal/store"
)

func fakeIn(t *testing.T, dir string) *Node {
	t.Helper()
	return newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1"}
    preamble: false
    cwd: %q
`, os.Args[0], dir))
}

// An agent whose directory is not a git repository: what its edit tool calls reported is what the
// turn changed (the Changes panel was empty there). A call that failed edited nothing.
func TestChangesOutsideGitAreTheReportedEdits(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	n := fakeIn(t, dir)
	a := ask(t, n, SendRequest{Body: `write sub/new.yaml a: 1\nb: 2\n`})
	b := ask(t, n, SendRequest{Body: `write old.txt one\nTWO\nthree\n`, Thread: a.Thread})
	f := ask(t, n, SendRequest{Body: `writefail lost.txt x\n`, Thread: a.Thread})

	turns, whole, err := n.Changes(ctx, me, a.ID, false, true)
	if err != nil || len(turns) != 1 || len(whole) != 1 {
		t.Fatalf("turns %v, whole %v, %v", turns, whole, err)
	}
	c := turns[0]
	if c.Repo != dir || c.Before != "" || len(c.Files) != 1 || c.Files[0].Path != "sub/new.yaml" || c.Files[0].Status != "A" || c.Files[0].Added != 2 {
		t.Errorf("a new file: %+v", c)
	}
	if !strings.Contains(c.Diff, "diff --git a/sub/new.yaml b/sub/new.yaml\nnew file mode 100644\n--- /dev/null\n+++ b/sub/new.yaml\n@@ -0,0 +1,2 @@\n+a: 1\n+b: 2\n") {
		t.Errorf("its diff:\n%s", c.Diff)
	}
	if whole[0].Repo != dir || whole[0].Diff != c.Diff || len(whole[0].Files) != 1 {
		t.Errorf("the whole: %+v", whole[0])
	}
	turns, _, _ = n.Changes(ctx, me, b.ID, false, true)
	if len(turns) != 1 || turns[0].Files[0].Status != "M" || turns[0].Files[0].Added != 1 || turns[0].Files[0].Removed != 1 ||
		!strings.Contains(turns[0].Diff, "--- a/old.txt\n+++ b/old.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n") {
		t.Errorf("an edit: %+v", turns)
	}
	if turns, _, _ := n.Changes(ctx, me, f.ID, false, true); len(turns) != 0 {
		t.Errorf("a failed call changed %+v", turns)
	}
	evs, _ := n.st.Events(ctx, a.ID, 0, 100)
	told := false
	for _, e := range evs {
		told = told || e.Kind == "changes" && strings.Contains(e.Data, "sub/new.yaml")
	}
	if !told {
		t.Error("the change was not told")
	}
}

// In a repository the snapshots tell what changed there (once, not twice); a file the agent edited
// outside it is told from its edit.
func TestChangesInGitAreNotToldTwice(t *testing.T) {
	repo, err := filepath.EvalSymlinks(testRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := filepath.EvalSymlinks(t.TempDir())
	n := fakeIn(t, repo)
	a := ask(t, n, SendRequest{Body: `write a.txt one\n2\n`})
	turns, whole, err := n.Changes(ctx, me, a.ID, false, true)
	if err != nil || len(turns) != 1 || turns[0].Before == "" || len(whole) != 1 {
		t.Fatalf("turns %+v, whole %+v, %v", turns, whole, err)
	}
	b := ask(t, n, SendRequest{Body: `write ` + filepath.Join(out, "far.txt") + ` x\n`, Thread: a.Thread})
	turns, whole, _ = n.Changes(ctx, me, b.ID, false, true)
	if len(turns) != 1 || turns[0].Before != "" || turns[0].Files[0].Path != filepath.Join(out, "far.txt") || len(whole) != 1 {
		t.Fatalf("outside the repository: %+v, %+v", turns, whole)
	}
}

func TestUnifiedHunk(t *testing.T) {
	for _, c := range []struct {
		old, new, want string
		added, removed int
	}{
		{"a\n", "a\n", "", 0, 0},
		{"", "a\nb\n", "@@ -0,0 +1,2 @@\n+a\n+b\n", 2, 0},
		{"a\nb\n", "", "@@ -1,2 +0,0 @@\n-a\n-b\n", 0, 2},
		{"1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n", "@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+X\n 6\n 7\n 8\n", 1, 1},
		{"a\na\n", "a\n", "@@ -1,2 +1,1 @@\n a\n-a\n", 0, 1},
		// The line end of the last line is part of it (finding 6).
		{"a", "a\n", "@@ -1,1 +1,1 @@\n-a\n\\ No newline at end of file\n+a\n", 1, 1},
		{"x\na", "x\nb", "@@ -1,2 +1,2 @@\n x\n-a\n\\ No newline at end of file\n+b\n\\ No newline at end of file\n", 1, 1},
		{"a\nend", "b\nend", "@@ -1,2 +1,2 @@\n-a\n+b\n end\n\\ No newline at end of file\n", 1, 1},
	} {
		got, added, removed := unifiedHunk(c.old, c.new)
		if got != c.want || added != c.added || removed != c.removed {
			t.Errorf("%q → %q: +%d -%d\n%s\nwant +%d -%d\n%s", c.old, c.new, added, removed, got, c.added, c.removed, c.want)
		}
	}
}

// An edit of a secret alone is an edit: the file is listed, its lines shown masked (finding 5).
func TestReportedSecretRotationIsAChange(t *testing.T) {
	files, diff, _ := reportedChanges("/w", "", []fileEdit{{path: "/w/.env", old: "A=1\nAPI_TOKEN=oldvalue123\n", new: "A=1\nAPI_TOKEN=newvalue456\n"}})
	if len(files) != 1 || files[0].Path != ".env" || files[0].Added != 1 || files[0].Removed != 1 {
		t.Fatalf("files: %+v", files)
	}
	if strings.Contains(diff, "oldvalue123") || strings.Contains(diff, "newvalue456") || !strings.Contains(diff, "-API_TOKEN=") || !strings.Contains(diff, "+API_TOKEN=") {
		t.Errorf("the diff:\n%s", diff)
	}
}

// A link outside the repository to a file in it is the repository's file: its snapshots tell it,
// the edit is not told again (finding 3). A file in a repository whose snapshot could not be taken
// is not told from its edit either (finding 2).
func TestReportedLeavesTheRepositoryToItsSnapshots(t *testing.T) {
	repo, _ := filepath.EvalSymlinks(testRepo(t))
	out, _ := filepath.EvalSymlinks(t.TempDir())
	link := filepath.Join(out, "link.txt")
	if err := os.Symlink(filepath.Join(repo, "a.txt"), link); err != nil {
		t.Fatal(err)
	}
	if files, _, _ := reportedChanges(out, repo, []fileEdit{{path: link, old: "one\n", new: "1\n"}}); len(files) != 0 {
		t.Errorf("a link into the repository: %+v", files)
	}
	if got := repoRoot(ctx, filepath.Join(repo)); got != repo {
		t.Errorf("the repository of %s: %q", repo, got)
	}
	if got := repoRoot(ctx, out); got != "" {
		t.Errorf("a plain directory's repository: %q", got)
	}
	n := fakeIn(t, repo)
	m := ask(t, n, SendRequest{Body: "echo hi"})
	n.recordChanges(m, "agent:fake", nil, repo, m.CreatedAt, []fileEdit{{path: filepath.Join(repo, "ignored", "x"), new: "s\n", created: true}}, false)
	if turns, _, _ := n.Changes(ctx, me, m.ID, false, true); len(turns) != 0 {
		t.Errorf("no snapshot, in a repository: %+v", turns)
	}
}

// A call's diff sent after its status counts (finding 1), and once: the same call saying it again
// replaces what it said.
func TestReportedLateContentCounts(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	n := fakeIn(t, dir)
	m := ask(t, n, SendRequest{Body: `writelate late.txt a\n`})
	turns, _, err := n.Changes(ctx, me, m.ID, false, true)
	if err != nil || len(turns) != 1 || len(turns[0].Files) != 1 || turns[0].Files[0].Path != "late.txt" || turns[0].Files[0].Added != 1 {
		t.Fatalf("turns: %+v, %v", turns, err)
	}
}

// Every edit of a call is taken, past what its event shows (finding 4); an edit too long to keep
// is listed without a diff, and the record says so (finding 9).
func TestReportedTakesAllEditsOfACall(t *testing.T) {
	turn := &acpTurn{}
	var content []acp.ToolCallContent
	for i := range 40 {
		content = append(content, acp.ToolCallContent{Diff: &acp.ToolCallContentDiff{Path: fmt.Sprintf("/w/f%02d", i), NewText: "x\n"}})
	}
	big := strings.Repeat("y\n", editKeepMax/2+1)
	content = append(content, acp.ToolCallContent{Diff: &acp.ToolCallContentDiff{Path: "/w/big", OldText: acp.Ptr("old\n"), NewText: big}})
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "c1", Status: acp.ToolCallStatusCompleted, Content: content})
	edits, more := turn.edits()
	if len(edits) != 41 || more {
		t.Fatalf("%d edits, more %v", len(edits), more)
	}
	files, diff, cut := reportedChanges("/w", "", edits)
	if len(files) != 41 || !cut || strings.Contains(diff, "b/big") || files[40].Path != "big" || files[40].Added == 0 {
		t.Errorf("%d files, cut %v, the last %+v", len(files), cut, files[len(files)-1])
	}
	// The same call again, with one edit: what it says now, not both.
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "c1", Content: content[:1]})
	if edits, _ := turn.edits(); len(edits) != 1 {
		t.Errorf("%d edits after the call said one", len(edits))
	}
}

// A message's whole is put together within the diff's bound (finding 7).
func TestReportedWholeIsBounded(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	n := fakeIn(t, dir)
	m := ask(t, n, SendRequest{Body: "echo hi"})
	section := func(name string) string {
		return "diff --git a/" + name + " b/" + name + "\n--- a/" + name + "\n+++ b/" + name + "\n@@ -0,0 +1,1 @@\n+" + strings.Repeat("z", 400<<10) + "\n"
	}
	for i, name := range []string{"a", "b", "c"} {
		c := store.TurnChanges{Msg: m.ID, Agent: "agent:fake", Start: m.CreatedAt, At: m.CreatedAt.Add(time.Duration(i) * time.Second), Repo: dir,
			Files: []store.ChangedFile{{Path: name, Status: "A", Added: 1}}, Diff: section(name)}
		if err := n.st.AddChanges(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	_, whole, err := n.Changes(ctx, me, m.ID, false, true)
	if err != nil || len(whole) != 1 {
		t.Fatalf("%+v, %v", whole, err)
	}
	if w := whole[0]; len(w.Diff) > changesDiffMax || !w.Cut || len(w.Files) != 3 || !strings.Contains(w.Diff, "b/b") || strings.Contains(w.Diff, "b/c\n") {
		t.Errorf("the whole: %d bytes, cut %v, %d files", len(w.Diff), w.Cut, len(w.Files))
	}
}

// Review round 2.

// Past the turn's count of edits nothing more is held: not the edits, not their calls.
func TestReportedEditsAreBoundedByCalls(t *testing.T) {
	turn := &acpTurn{}
	one := []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: "/w/f", NewText: "x\n"}}}
	for i := range editsMax + 500 {
		turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: acp.ToolCallId(fmt.Sprintf("c%d", i)), Status: acp.ToolCallStatusCompleted, Content: one})
	}
	edits, more := turn.edits()
	if len(edits) != editsMax || !more || len(turn.edited) != editsMax || len(turn.editedOrder) != editsMax {
		t.Errorf("%d edits, more %v, %d calls held, %d in order", len(edits), more, len(turn.edited), len(turn.editedOrder))
	}
	// A call already held may still say otherwise.
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "c0", Content: []acp.ToolCallContent{}})
	if edits, _ := turn.edits(); len(edits) != editsMax-1 || len(turn.editedOrder) != editsMax-1 {
		t.Errorf("%d edits after one call took its edit back", len(edits))
	}
}

// A call that says in the end it edited nothing (content sent, empty) edited nothing: before its
// status or after it. Text alone leaves its edits (claude-agent-acp ends an edit so).
func TestReportedEmptyContentTakesEditsBack(t *testing.T) {
	diff := []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: "/w/f", NewText: "x\n"}}}
	done := acp.ToolCallStatusCompleted
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "a", Content: diff})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "a", Status: &done, Content: []acp.ToolCallContent{}})
	if edits, _ := turn.edits(); len(edits) != 0 {
		t.Errorf("a tentative diff, then none: %+v", edits)
	}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "b", Status: done, Content: diff})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "b", Content: []acp.ToolCallContent{}})
	if edits, _ := turn.edits(); len(edits) != 0 {
		t.Errorf("completed with a diff, then none: %+v", edits)
	}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "c", Content: diff})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "c", Status: &done, Content: []acp.ToolCallContent{acp.ToolContent(acp.TextBlock("edited"))}})
	if edits, _ := turn.edits(); len(edits) != 1 {
		t.Errorf("a diff, then its result's text: %+v", edits)
	}
}

// A link in the repository to a file outside it is that file: the snapshots have the link alone,
// so the edit is told from the call.
func TestReportedFollowsALinkOutOfTheRepository(t *testing.T) {
	repo, _ := filepath.EvalSymlinks(testRepo(t))
	out, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(out, "far.txt"), []byte("1\n"), 0o644)
	if err := os.Symlink(filepath.Join(out, "far.txt"), filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	files, _, _ := reportedChanges(repo, repo, []fileEdit{{path: filepath.Join(repo, "link.txt"), old: "0\n", new: "1\n"}, {path: filepath.Join(repo, "a.txt"), old: "one\n", new: "1\n"}})
	if len(files) != 1 || files[0].Path != "link.txt" {
		t.Errorf("files: %+v", files)
	}
}

// An edit inside a masked value of several lines: said where, its lines not shown, counted.
func TestReportedRotationOfASecretOverLines(t *testing.T) {
	old := "{\n  \"api_key\": [\n    \"aaaaaaaaaaaa\",\n    \"bbbbbbbbbbbb\"\n  ],\n  \"n\": 1\n}\n"
	new := strings.Replace(old, "bbbbbbbbbbbb", "cccccccccccc", 1)
	if redact(old) != redact(new) || len(endedLines(redact(old))) == len(endedLines(old)) {
		t.Skip("masking no longer folds this value: another example is needed")
	}
	hunk, added, removed := unifiedHunk(old, new)
	if added != 1 || removed != 1 || strings.Contains(hunk, "bbbb") || strings.Contains(hunk, "cccc") || hunk != "@@ -4,1 +4,1 @@\n-"+maskedLine+"\n+"+maskedLine+"\n" {
		t.Errorf("+%d -%d\n%s", added, removed, hunk)
	}
	files, diff, _ := reportedChanges("/w", "", []fileEdit{{path: "/w/c.json", old: old, new: new}})
	if len(files) != 1 || files[0].Added != 1 || files[0].Removed != 1 || !strings.Contains(diff, maskedLine) {
		t.Errorf("files %+v\n%s", files, diff)
	}
}

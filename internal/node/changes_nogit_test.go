package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, c := range []struct{ old, new, want string }{
		{"a\n", "a\n", ""},
		{"", "a\nb\n", "@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{"a\nb\n", "", "@@ -1,2 +0,0 @@\n-a\n-b\n"},
		{"1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n", "@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+X\n 6\n 7\n 8\n"},
		{"a\na\n", "a\n", "@@ -1,2 +1,1 @@\n a\n-a\n"},
	} {
		if got := unifiedHunk(c.old, c.new); got != c.want {
			t.Errorf("%q → %q:\n%s\nwant\n%s", c.old, c.new, got, c.want)
		}
	}
}

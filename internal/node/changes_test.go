package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func testRepo(t *testing.T) string {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n"), 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "init")
	return dir
}

// A turn's changes: modified, added (untracked), deleted, renamed; ignored files are not; the
// repository's index and its status are as they were.
func TestSnapshotDiff(t *testing.T) {
	dir := testRepo(t)
	os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("s\n"), 0o644)
	gitIn(t, dir, "add", "staged.txt") // the user's own staged work
	status := gitIn(t, dir, "status", "--porcelain")
	index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))

	before := snapshot(ctx, filepath.Join(dir, "."))
	if before == nil || before.branch != "main" {
		t.Fatalf("%+v", before)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nTWO\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644)
	os.Remove(filepath.Join(dir, "gone.txt"))
	os.MkdirAll(filepath.Join(dir, "ignored"), 0o755)
	os.WriteFile(filepath.Join(dir, "ignored", "big.bin"), []byte("ignored"), 0o644)
	after := snapshot(ctx, dir)
	files, diff, cut, err := diffSnaps(ctx, before, after)
	if err != nil || cut {
		t.Fatal(err, cut)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Status + " " + lineStat(f.Added, f.Removed)
	}
	want := map[string]string{"a.txt": "M +2 -1", "new.txt": "A +1 -0", "gone.txt": "D +0 -1"}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %q, want %q (%v)", k, got[k], v, got)
		}
	}
	if _, ok := got["ignored/big.bin"]; ok || !strings.Contains(diff, "+TWO") {
		t.Fatalf("%v\n%s", got, diff)
	}
	if s := gitIn(t, dir, "status", "--porcelain"); !strings.Contains(s, "A  staged.txt") || strings.Contains(s, "A  new.txt") {
		t.Fatalf("the user's index changed:\nbefore %s\nafter %s", status, s)
	}
	if now, _ := os.ReadFile(filepath.Join(dir, ".git", "index")); string(now) != string(index) {
		t.Fatal("the index file was written")
	}
	if snapshot(ctx, t.TempDir()) != nil {
		t.Fatal("a snapshot outside a repository")
	}
}

func lineStat(a, r int) string { return "+" + itoa(a) + " -" + itoa(r) }

// The agent's repository names commands (a hook, fsmonitor, a filter, a textconv): a snapshot runs
// none of them.
func TestSnapshotRunsNothingOfTheRepository(t *testing.T) {
	dir := testRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho ran >> "+marker+"\ncat\n"), 0o755)
	hooks := filepath.Join(dir, "hooks")
	os.MkdirAll(hooks, 0o755)
	for _, h := range []string{"post-index-change", "pre-commit"} {
		os.WriteFile(filepath.Join(hooks, h), []byte("#!/bin/sh\necho ran >> "+marker+"\n"), 0o755)
	}
	gitIn(t, dir, "config", "core.hooksPath", hooks)
	gitIn(t, dir, "config", "core.fsmonitor", script)
	gitIn(t, dir, "config", "filter.evil.clean", script)
	gitIn(t, dir, "config", "filter.evil.required", "true")
	gitIn(t, dir, "config", "diff.evil.textconv", script)
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=evil diff=evil\n"), 0o644)
	before := snapshot(ctx, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644)
	after := snapshot(ctx, dir)
	if before == nil || after == nil {
		t.Fatal("no snapshot")
	}
	if _, diff, _, err := diffSnaps(ctx, before, after); err != nil || !strings.Contains(diff, "+changed") {
		t.Fatalf("%v %s", err, diff)
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the repository's commands ran: %s", b)
	}
}

// Too much untracked to take into the repository: no snapshot.
func TestSnapshotSkipsHugeUntracked(t *testing.T) {
	dir := testRepo(t)
	f, _ := os.Create(filepath.Join(dir, "dataset.bin"))
	f.Truncate(snapshotBytes + 1)
	f.Close()
	if snapshot(ctx, dir) != nil {
		t.Fatal("a snapshot of 64 MiB untracked")
	}
}

func TestChangesForTheNodesUser(t *testing.T) {
	n := newNode(t, inboxCfg)
	if _, _, err := n.Changes(ctx, Principal{Kind: "client", Name: "ci"}, "msg_x", false, false); code(err) != "refused" {
		t.Fatalf("%v", err)
	}
}

// A filter whose name has "=" in it is a pass-through too; a diff is cut between files.
func TestSnapshotDefuseOddNames(t *testing.T) {
	dir := testRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho ran >> "+marker+"\ncat\n"), 0o755)
	gitIn(t, dir, "config", "filter.x=y.clean", script)
	gitIn(t, dir, "config", "filter.x=y.required", "true")
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=x=y\n"), 0o644)
	before := snapshot(ctx, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644)
	if snapshot(ctx, dir) == nil || before == nil {
		t.Fatal("no snapshot")
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("ran: %s", b)
	}
	d := "diff --git a/a b/a\n+1\ndiff --git a/b b/b\n+" + strings.Repeat("x", 100) + "\n"
	if got := wholeFiles(d, 50); got != "diff --git a/a b/a\n+1\n" {
		t.Fatalf("%q", got)
	}
}

package node

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A new file git cannot read is named as left out, not dropped from a patch sent as complete.
func TestHandoffNamesUnreadableNewFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 000")
	}
	dir := handoffRepo(t)
	locked := filepath.Join(dir, "locked.go")
	if err := os.WriteFile(locked, []byte("package a // locked\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o600) })
	rs, err := readRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(rs.left, "locked.go") {
		t.Fatalf("the unreadable file is not named as left out: %v", rs.left)
	}
	if strings.Contains(string(rs.patch), "locked.go") || !strings.Contains(string(rs.patch), "package a // new") {
		t.Fatalf("the patch:\n%s", rs.patch)
	}
}

// git's answers for a new file, as it gives them: a patch, a file gone since it was listed (exit
// 1, nothing out), and a git that did not finish.
func TestNewFilePatchKinds(t *testing.T) {
	dir := handoffRepo(t)
	diff := func(f string) ([]byte, error) {
		out, _, err := git(ctx, dir, nil, 1<<20, append(append([]string{}, gitSafe...), "diff", "--no-index", "--binary", "--no-ext-diff", "--no-textconv", "--", "/dev/null", f)...)
		return out, err
	}
	if got := newFilePatch(diff("new.go")); got != patchTaken {
		t.Errorf("a new file: %d", got)
	}
	if got := newFilePatch(diff("gone.go")); got != patchLeft {
		t.Errorf("a file gone since listed: %d", got)
	}
	if got := newFilePatch([]byte("diff --git a/x b/x\nnew file"), errors.New("signal: killed")); got != patchFailed {
		t.Errorf("git killed in the middle: %d", got)
	}
}

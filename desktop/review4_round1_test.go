//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A file of mode 0000 saved over stays 0000, its temporary copy too: 0000 is a mode, not "no
// mode to keep".
func TestSaveKeepsModeZero(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "locked.txt")
	os.WriteFile(dest, []byte("old"), 0o600)
	os.Chmod(dest, 0)
	t.Cleanup(func() { os.Chmod(dest, 0o600) })
	seen := os.FileMode(0o777)
	old := afterWrite
	afterWrite = func(tmp string) {
		if st, err := os.Stat(tmp); err == nil {
			seen = st.Mode().Perm()
		}
	}
	t.Cleanup(func() { afterWrite = old })
	if err := saveFile(dest, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if seen != 0 {
		t.Fatalf("the temporary file was %v, want 0000", seen)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0 {
		t.Fatalf("saved: %v, want 0000", st.Mode())
	}
}

// Saving over a link writes the file it points to, as os.WriteFile did: the link stays a link.
func TestSaveThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "real"), 0o755)
	target := filepath.Join(dir, "real", "report.txt")
	os.WriteFile(target, []byte("old"), 0o644)
	link := filepath.Join(dir, "report.txt")
	if err := os.Symlink("real/report.txt", link); err != nil {
		t.Fatal(err)
	}
	if err := saveFile(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v %v", st.Mode(), err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatalf("the target has %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "real", ".acpost-tmp-*")); len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

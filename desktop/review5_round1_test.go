//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Saving through a chain of links to a file not there yet makes that file: every link of the
// chain stays a link. A cycle of links is an error, not a file written somewhere.
func TestSaveThroughLinkChainToMissingFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "real"), 0o755)
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.Symlink("b", a)
	os.Symlink("real/report.txt", b)
	if err := saveFile(a, []byte("new")); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{a, b} {
		if st, err := os.Lstat(l); err != nil || st.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is no longer a link: %v %v", filepath.Base(l), st.Mode(), err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "real", "report.txt")); string(got) != "new" {
		t.Fatalf("the chain's end has %q", got)
	}
	c, d := filepath.Join(dir, "c"), filepath.Join(dir, "d")
	os.Symlink("d", c)
	os.Symlink("c", d)
	if err := saveFile(c, []byte("x")); err == nil {
		t.Fatal("a cycle of links saved")
	}
	for _, l := range []string{c, d} {
		if st, err := os.Lstat(l); err != nil || st.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s of the cycle was replaced", filepath.Base(l))
		}
	}
}

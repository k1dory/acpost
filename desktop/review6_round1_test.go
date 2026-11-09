//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A link's ".." goes up from where its symlinked directories lead, as the system reads it,
// not from the text of the path: no file beside the link is written by mistake. A ".." after a
// directory that is not there is an error.
func TestSaveThroughLinkWithDotDot(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "real", "nested"), 0o755)
	os.Symlink("real/nested", filepath.Join(dir, "sub"))
	foreign := filepath.Join(dir, "report.txt")
	os.WriteFile(foreign, []byte("foreign"), 0o644)
	link := filepath.Join(dir, "link")
	os.Symlink("sub/../report.txt", link) // the system reads it as real/report.txt
	if err := saveFile(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "foreign" {
		t.Fatalf("the file beside the link was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "real", "report.txt")); string(b) != "new" {
		t.Fatalf("real/report.txt has %q", b)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}

	other := filepath.Join(dir, "other.txt")
	os.WriteFile(other, []byte("foreign"), 0o644)
	bad := filepath.Join(dir, "bad")
	os.Symlink("missing/../other.txt", bad)
	if err := saveFile(bad, []byte("new")); err == nil {
		t.Fatal("a link through a missing directory saved")
	}
	if b, _ := os.ReadFile(other); string(b) != "foreign" {
		t.Fatalf("other.txt was overwritten: %q", b)
	}
}

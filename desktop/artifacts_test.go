//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A file of this Mac shows; one in a hidden folder, or reached through a link into one, does not.
func TestLocalFile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "report.html"), []byte("<h1>ok</h1>"), 0o644)
	os.MkdirAll(filepath.Join(root, ".secret"), 0o755)
	os.WriteFile(filepath.Join(root, ".secret", "key.txt"), []byte("k"), 0o644)
	os.Symlink(filepath.Join(root, ".secret"), filepath.Join(root, "open"))
	n := &Node{}
	a, err := n.LocalFile("report.html", root)
	if err != nil || a.Name != "report.html" || a.Type != "text/html; charset=utf-8" {
		t.Fatalf("%+v %v", a, err)
	}
	for _, bad := range []string{".secret/key.txt", "open/key.txt", "../" + filepath.Base(root) + "/.secret/key.txt", "/etc/../" + root[1:] + "/.secret/key.txt"} {
		if _, err := n.LocalFile(bad, root); err == nil {
			t.Fatalf("%s was shown", bad)
		}
	}
	if typeOf("a.ts", "", nil) != "text/plain; charset=utf-8" {
		t.Fatal(typeOf("a.ts", "", nil))
	}
}

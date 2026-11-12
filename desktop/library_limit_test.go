//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A file over artifactMax is refused by a card's preview as LocalFile refuses it (review round 1).
func TestPreviewKeepsLocalFilesLimit(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "big.md")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# big\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(artifactMax + 1); err != nil { // sparse: no 20 MiB written
		t.Fatal(err)
	}
	f.Close()
	n := &Node{}
	if _, err := n.LocalFile(p, ""); err != errArtifact {
		t.Fatalf("LocalFile: %v", err)
	}
	if a, err := localPreview(p); err != errArtifact {
		t.Fatalf("preview: %+v %v", a.Size, err)
	}
	if err := os.Truncate(p, artifactMax); err != nil {
		t.Fatal(err)
	}
	if a, err := localPreview(p); err != nil || a.Data == "" {
		t.Fatalf("at the limit: %v", err)
	}
}

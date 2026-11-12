//go:build darwin

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// A node whose replies hold a small picture, a big one, a text and a page; it says which heads it
// was asked for.
func libraryNode(t *testing.T) (*Node, *[]string) {
	small := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, previewText)...) // more than one head
	files := map[string][]byte{
		"small.png": small,
		"big.png":   append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, previewImage)...),
		"notes.md":  bytes.Repeat([]byte("line\n"), 4000),
		"page.html": []byte("<script>x()</script>"),
	}
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/artifacts", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, "list "+r.URL.RawQuery)
		json.NewEncoder(w).Encode(client.Artifacts{Items: []store.Artifact{{Kind: "file", Name: "small.png"}}, Next: "n"})
	})
	mux.HandleFunc("GET /v1/artifacts/head", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		max, _ := strconv.Atoi(r.URL.Query().Get("max"))
		asked = append(asked, name+" "+strconv.Itoa(max))
		b := files[name]
		json.NewEncoder(w).Encode(store.File{Name: name, Size: len(b), Data: b[:min(max, len(b))], Reply: true})
	})
	n, _ := fakeNode(t, mux)
	return n, &asked
}

func TestArtifactPreviewOfReplyFiles(t *testing.T) {
	n, asked := libraryNode(t)
	page, err := n.Artifacts("q", "b", 3)
	if err != nil || len(page.Items) != 1 || page.Next != "n" {
		t.Fatalf("%+v %v", page, err)
	}
	data := func(a Artifact) int { b, _ := base64.StdEncoding.DecodeString(a.Data); return len(b) }

	// A small picture is shown whole: a second head for the rest.
	a, err := n.ArtifactPreview("m", "small.png", "")
	if err != nil || a.Type != "image/png" || data(a) != a.Size {
		t.Fatalf("small picture %+v %v", a.Type, err)
	}
	// A big one by its name only, a text by its start, a page not at all.
	if a, _ := n.ArtifactPreview("m", "big.png", ""); a.Data != "" || a.Size <= previewImage {
		t.Errorf("big picture: %d bytes shown", data(a))
	}
	if a, _ := n.ArtifactPreview("m", "notes.md", ""); data(a) != previewText || a.Size != 20000 {
		t.Errorf("text: %d of %d", data(a), a.Size)
	}
	if a, _ := n.ArtifactPreview("m", "page.html", ""); a.Data != "" || a.Type != "text/html; charset=utf-8" {
		t.Errorf("page: %q %q", a.Type, a.Data)
	}
	want := []string{"list before=b&limit=3&q=q", "small.png 8192", "small.png 2097152", "big.png 8192", "notes.md 8192", "page.html 8192"}
	if len(*asked) != len(want) {
		t.Fatalf("asked %q", *asked)
	}
	for i := range want {
		if (*asked)[i] != want[i] {
			t.Fatalf("asked %q, want %q", *asked, want)
		}
	}
}

func TestArtifactPreviewOfLocalFiles(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	md := write("a.md", bytes.Repeat([]byte("x"), previewText+10))
	a, err := localPreview(md)
	if b, _ := base64.StdEncoding.DecodeString(a.Data); err != nil || len(b) != previewText || a.Size != previewText+10 || a.Path != md {
		t.Fatalf("text %+v %v", a.Size, err)
	}
	gif := write("a.gif", []byte("GIF89a....."))
	if a, err := localPreview(gif); err != nil || a.Type != "image/gif" || a.Data == "" {
		t.Fatalf("picture %+v %v", a, err)
	}
	pdf := write("a.pdf", []byte("%PDF-1.4"))
	if a, err := localPreview(pdf); err != nil || a.Data != "" || a.Type != "application/pdf" {
		t.Fatalf("pdf %+v %v", a, err)
	}
	// As LocalFile: nothing in a hidden folder, no link.
	if err := os.Mkdir(filepath.Join(dir, ".secret"), 0o700); err != nil {
		t.Fatal(err)
	}
	hidden := write(".secret/k.md", []byte("key"))
	if _, err := localPreview(hidden); err == nil {
		t.Error("a hidden folder's file was shown")
	}
	link := filepath.Join(dir, "l.md")
	if err := os.Symlink(hidden, link); err != nil {
		t.Fatal(err)
	}
	if _, err := localPreview(link); err == nil {
		t.Error("a link into a hidden folder was shown")
	}
	if _, err := localPreview(filepath.Join(dir, "missing.md")); err == nil {
		t.Error("a missing file was shown")
	}
}

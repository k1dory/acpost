//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The agents' working folders give their pages, reports and pictures, newest first: a site once,
// by its index.html; nothing hidden, of node_modules, empty, of another type, or behind a link.
func TestFoundArtifacts(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(top, "tmp")
	home, _ := os.UserHomeDir()
	proj := filepath.Join(root, strings.ReplaceAll(home, "/", "-")+"-site")
	put := func(rel, body string, age time.Duration) string {
		p := filepath.Join(proj, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		_ = os.Chtimes(p, at, at)
		return p
	}
	report := put("s1/scratchpad/report.pdf", "%PDF", 3*time.Hour)
	shot := put("s1/images/shot.PNG", "png", 2*time.Hour)
	index := put("s1/scratchpad/landing/index.html", "<html>", time.Hour)
	put("s1/scratchpad/landing/about.html", "<html>", time.Minute)
	put("s1/scratchpad/landing/img/hero.png", "png", time.Minute)
	doc := put("s1/scratchpad/notes/plan.docx", "PK", 4*time.Hour)
	put("s1/scratchpad/node_modules/x/readme.html", "<html>", time.Minute)
	put("s1/scratchpad/.cache/a.png", "png", time.Minute)
	put("s1/scratchpad/main.go", "package x", time.Minute)
	put("s1/scratchpad/empty.pdf", "", time.Minute)
	outside := filepath.Join(top, "outside")
	_ = os.MkdirAll(outside, 0o755)
	_ = os.WriteFile(filepath.Join(outside, "secret.pdf"), []byte("x"), 0o644)
	_ = os.Symlink(outside, filepath.Join(proj, "s1", "scratchpad", "linked"))
	_ = os.Symlink(filepath.Join(outside, "secret.pdf"), filepath.Join(proj, "s1", "scratchpad", "linked.pdf"))

	old := foundRoots
	foundRoots = func() []foundRoot {
		return []foundRoot{{root, "agent:claude", true}, {filepath.Join(top, "missing"), "agent:codex", false}}
	}
	t.Cleanup(func() { foundRoots = old })

	n := &Node{}
	got, err := n.FoundArtifacts("")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, a := range got {
		paths = append(paths, a.Path)
	}
	want := []string{index, shot, report, doc}
	if len(paths) != len(want) {
		t.Fatalf("%v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("at %d: %v", i, paths)
		}
	}
	for _, a := range got {
		if a.Path == index && (a.Name != "landing/index.html" || a.Title != "site" || a.Agent != "agent:claude" || a.Kind != "change") {
			t.Fatalf("%+v", a)
		}
	}
	if got, _ := n.FoundArtifacts("REPORT"); len(got) != 1 || got[0].Path != report {
		t.Fatalf("filtered: %+v", got)
	}
	if got, _ := n.FoundArtifacts("nothing-like-it"); got == nil || len(got) != 0 {
		t.Fatalf("none is an empty list: %#v", got)
	}
}

// The pictures Codex generated are in its hidden home: they are listed, a card and the viewer
// may read them, and nothing else of that home, nor anything hidden under the pictures' folder.
func TestFoundCodexImages(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	codex := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codex)
	pic := filepath.Join(codex, "generated_images", "thread_1", "call_1.png")
	hidden := filepath.Join(codex, "generated_images", ".x", "a.png")
	auth := filepath.Join(codex, "auth.json")
	for _, p := range []string{pic, hidden, auth} {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := (&Node{}).FoundArtifacts("call_1")
	if len(got) != 1 || got[0].Path != pic || got[0].Agent != "agent:codex" || got[0].Title != "" {
		t.Fatalf("%+v", got)
	}
	if p, err := artifactPath(pic, ""); err != nil || p != pic {
		t.Fatalf("a generated picture: %q %v", p, err)
	}
	for _, p := range []string{hidden, auth, filepath.Join(codex, "generated_images", "..", "auth.json"), filepath.Join(codex, "generated_images2", "a.png")} {
		if _, err := artifactPath(p, ""); err == nil {
			t.Errorf("%s was allowed", p)
		}
	}
	link := filepath.Join(codex, "generated_images", "thread_1", "link.png")
	_ = os.Symlink(auth, link)
	if _, err := artifactPath(link, ""); err == nil {
		t.Error("a link out of the pictures' folder was followed")
	}
}

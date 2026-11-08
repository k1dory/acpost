//go:build darwin

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPackExtension(t *testing.T) {
	fsys := fstest.MapFS{
		"x/package.json":      {Data: []byte(`{"name":"acpost-ide","version":"1.2.3"}`)},
		"x/themes/a.json":     {Data: []byte(`{}`)},
		"x/fonts/onest.woff2": {Data: []byte{0, 1, 2}},
	}
	p := packExtension(fsys, "x")
	if p.err != nil || p.version != "1.2.3" || len(p.hash) != 64 {
		t.Fatalf("pack: %+v", p.err)
	}
	z, err := zip.NewReader(bytes.NewReader(p.vsix), int64(len(p.vsix)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range z.File {
		names[f.Name] = true
	}
	for _, n := range []string{"extension/package.json", "extension/themes/a.json", "extension/fonts/onest.woff2"} {
		if !names[n] {
			t.Errorf("%s not in the vsix (%v)", n, names)
		}
	}
	// A changed file is a new hash; the same files, the same one.
	if q := packExtension(fsys, "x"); q.hash != p.hash {
		t.Error("hash not stable")
	}
	fsys["x/themes/a.json"] = &fstest.MapFile{Data: []byte(`{"b":1}`)}
	if q := packExtension(fsys, "x"); q.hash == p.hash {
		t.Error("hash did not change")
	}
	delete(fsys, "x/package.json")
	if q := packExtension(fsys, "x"); q.err == nil {
		t.Error("no package.json: no error")
	}
}

// The embedded extension is the built one: a manifest with its themes and icons, and the files
// they name.
func TestEmbeddedExtension(t *testing.T) {
	p := ideExtension()
	if p.err != nil {
		t.Fatal(p.err)
	}
	var m struct {
		Main        string
		Contributes struct {
			Themes            []struct{ Path string }
			ProductIconThemes []struct{ ID, Path string }
		}
	}
	b, _ := ideExtFS.ReadFile("ide/extension/package.json")
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	files := []string{m.Main, "workbench.css", "fonts/onest-latin.woff2", "fonts/jetbrains-mono-cyrillic-400.woff2"}
	for _, th := range m.Contributes.Themes {
		files = append(files, th.Path)
	}
	for _, th := range m.Contributes.ProductIconThemes {
		files = append(files, th.Path, "icons/acpost.woff2")
	}
	if len(m.Contributes.Themes) != 2 || len(m.Contributes.ProductIconThemes) != 1 {
		t.Fatalf("contributes: %+v", m.Contributes)
	}
	for _, f := range files {
		if _, err := ideExtFS.ReadFile(filepath.Join("ide/extension", filepath.Clean(f))); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// Installed once: the same extension again runs nothing; a changed one runs VS Code's installer.
func TestIDEInstallOnce(t *testing.T) {
	data := t.TempDir()
	bin := filepath.Join(data, "cli", "serve-web", ideCommit, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(data, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\nmkdir -p \"$2/extensions/acpost.acpost-ide-" + ideExtension().version + "\"\n"
	if err := os.WriteFile(filepath.Join(bin, "code-server"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ideInstall(context.Background(), data); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(log)
	if n := bytes.Count(b, []byte("--install-extension")); n != 1 {
		t.Fatalf("installer ran %d times: %s", n, b)
	}
	os.WriteFile(filepath.Join(ideExtDir(data, ideExtension().version), ".acpost-hash"), []byte("old"), 0o600)
	if err := ideInstall(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(log)
	if n := bytes.Count(b, []byte("--install-extension")); n != 2 {
		t.Fatalf("a changed extension: installer ran %d times in all", n)
	}
}

// The page's scripts are JavaScript (checked by node, when there is one).
func TestIDEScriptsParse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	start, seed := ideScriptText("/Users/x/Library/Application Support/acpost/vscode/server/extensions/acpost.acpost-ide-1.0.0", strings.Repeat("ab", 32))
	for name, js := range map[string]string{"start": start, "seed": seed} {
		f := filepath.Join(t.TempDir(), name+".js")
		os.WriteFile(f, []byte(js), 0o600)
		if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", name, err, out)
		}
	}
	if !strings.Contains(start, "Application+Support") || !strings.Contains(start, "workbench.css") {
		t.Errorf("start script links: %s", start)
	}
}

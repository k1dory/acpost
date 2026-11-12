//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modulesIn(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conf", "modules.json")
	old, oldForget := modulesPath, forgetModule
	modulesPath = func() string { return path }
	t.Cleanup(func() { modulesPath, forgetModule = old, oldForget })
	return path
}

func TestModulesAreKept(t *testing.T) {
	path := modulesIn(t)
	var forgot []string
	forgetModule = func(id string) { forgot = append(forgot, id) }
	n := &Node{}
	if ms, err := n.Modules(); err != nil || len(ms) != 0 {
		t.Fatalf("none yet: %v %v", ms, err)
	}
	wiki, err := n.PutModule(Module{Name: " Wiki ", URL: "wiki.example.com/start"})
	if err != nil || wiki.ID == "" || wiki.Name != "Wiki" || wiki.URL != "https://wiki.example.com/start" {
		t.Fatalf("%+v %v", wiki, err)
	}
	local, err := n.PutModule(Module{Name: "Board", URL: "http://localhost:3000"})
	if err != nil || local.ID == wiki.ID {
		t.Fatalf("%+v %v", local, err)
	}
	wiki.URL = "https://wiki.example.com/other"
	if _, err := n.PutModule(wiki); err != nil {
		t.Fatal(err)
	}
	ms, _ := n.Modules()
	if len(ms) != 2 || ms[0].URL != "https://wiki.example.com/other" || ms[1].Name != "Board" {
		t.Fatalf("%+v", ms)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %v %v", st, err)
	}
	if err := n.DeleteModule(wiki.ID); err != nil {
		t.Fatal(err)
	}
	if ms, _ := n.Modules(); len(ms) != 1 || ms[0].ID != local.ID || len(forgot) != 1 || forgot[0] != wiki.ID {
		t.Fatalf("%+v, forgot %v", ms, forgot)
	}
	if _, err := n.PutModule(wiki); err == nil {
		t.Fatal("a module removed was changed")
	}
}

func TestModulesRefused(t *testing.T) {
	modulesIn(t)
	n := &Node{}
	for name, m := range map[string]Module{
		"no name":        {URL: "https://a.example"},
		"a long name":    {Name: strings.Repeat("я", maxModuleName+1), URL: "https://a.example"},
		"a file":         {Name: "a", URL: "file:///etc/passwd"},
		"a script":       {Name: "a", URL: "javascript://x/%0aalert(1)"},
		"the app":        {Name: "a", URL: "wails://localhost/"},
		"no host":        {Name: "a", URL: "https:///x"},
		"a password":     {Name: "a", URL: "https://me:secret@a.example"},
		"a long address": {Name: "a", URL: "https://a.example/" + strings.Repeat("x", maxModuleURL)},
	} {
		if got, err := n.PutModule(m); err == nil {
			t.Errorf("%s was taken: %+v", name, got)
		}
	}
	if _, err := n.PutModule(Module{Name: "Wiki", URL: "https://a.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.PutModule(Module{Name: "wiki", URL: "https://b.example"}); err == nil {
		t.Error("two modules of one name")
	}
	if ms, _ := n.Modules(); len(ms) != 1 {
		t.Fatalf("%+v", ms)
	}
}

// The interface names a module; the address shown is the one kept for it.
func TestModuleShowOpensTheKeptAddress(t *testing.T) {
	modulesIn(t)
	old := showModule
	t.Cleanup(func() { showModule = old })
	var shown Module
	showModule = func(_ *Node, m Module, x, y, w, h float64, dark bool) error { shown = m; return nil }
	n := &Node{}
	m, _ := n.PutModule(Module{Name: "Wiki", URL: "https://wiki.example.com"})
	if err := n.ModuleShow(m.ID, 0, 0, 10, 10, true); err != nil || shown.URL != "https://wiki.example.com" {
		t.Fatalf("%+v %v", shown, err)
	}
	if err := n.ModuleShow("mod_none", 0, 0, 10, 10, true); err == nil {
		t.Fatal("a module that is not there was shown")
	}
}

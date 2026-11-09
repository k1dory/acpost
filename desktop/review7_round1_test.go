//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// listing is every name under dir with its contents, to see that a refused save changed nothing.
func listing(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	filepath.Walk(dir, func(p string, st os.FileInfo, err error) error {
		if err == nil && st.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			m[p] = string(b)
		} else if err == nil {
			m[p] = st.Mode().Type().String()
		}
		return nil
	})
	return m
}

// A save through a link reaches what the system reaches: a trailing "/." or "/" says "a
// directory", so a link to report.txt/. or report.txt/ does not overwrite report.txt and
// missing/. makes no file; a link to ".", ".." or a folder is not a file either.
func TestSaveThroughLinkPathForms(t *testing.T) {
	refused := []string{"report.txt/.", "report.txt/", "report.txt//", "missing/.", "missing/",
		"sub/missing/.", ".", "..", "sub", "sub/", "sub/.."}
	for _, to := range refused {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "report.txt"), []byte("old"), 0o644)
		os.Mkdir(filepath.Join(dir, "sub"), 0o755)
		link := filepath.Join(dir, "link")
		os.Symlink(to, link)
		before := listing(t, filepath.Dir(dir))
		if err := saveFile(link, []byte("new")); err == nil {
			t.Errorf("a link to %q saved", to)
		}
		if after := listing(t, filepath.Dir(dir)); !same(before, after) {
			t.Errorf("a link to %q changed files: %v -> %v", to, keys(before), keys(after))
		}
	}

	// Forms the system writes as a file: where the file lands.
	written := []struct{ to, lands string }{
		{"report.txt", "report.txt"},
		{"sub//report.txt", "sub/report.txt"},
		{"./sub/./report.txt", "sub/report.txt"},
		{"sub/../report.txt", "report.txt"},
		{"new.txt", "new.txt"},         // a dangling link: the file is made at its end
		{"sub/new.txt", "sub/new.txt"}, // dangling, through a folder
		{"ABS", "sub/abs.txt"},         // an absolute target
		{"ABS//", ""},                  // an absolute target with a trailing slash: refused
	}
	for _, w := range written {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "report.txt"), []byte("old"), 0o644)
		os.Mkdir(filepath.Join(dir, "sub"), 0o755)
		to := w.to
		if to == "ABS" {
			to = filepath.Join(dir, "sub", "abs.txt")
		} else if to == "ABS//" {
			to = filepath.Join(dir, "sub", "abs.txt") + "//"
		}
		link := filepath.Join(dir, "link")
		os.Symlink(to, link)
		err := saveFile(link, []byte("new"))
		if w.lands == "" {
			if err == nil {
				t.Errorf("a link to %q saved", to)
			}
			continue
		}
		if err != nil {
			t.Errorf("a link to %q: %v", to, err)
			continue
		}
		if b, _ := os.ReadFile(filepath.Join(dir, w.lands)); string(b) != "new" {
			t.Errorf("a link to %q: %s holds %q", to, w.lands, b)
		}
		if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
			t.Errorf("a link to %q is no longer a link", to)
		}
	}
}

func same(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

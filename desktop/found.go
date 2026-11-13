//go:build darwin

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// Found artifacts: what the agents of this Mac left in their own working folders outside any
// chat's directory — Claude Code's and Codex's under /private/tmp (a session's scratchpad, the
// pictures it took), and the pictures Codex generated, which it keeps in its own home
// ($CODEX_HOME/generated_images/<thread>/<call>.png, as its image tool documents; everything else
// it writes goes into the task's directory). Pages, reports and pictures only; a site is listed
// once, by its index.html.

const (
	foundMax    = 1500            // the newest listed
	foundVisits = 200000          // entries looked at, at most
	foundTime   = 4 * time.Second // and for this long
	foundDepth  = 14              // folders deep under a root
)

// foundExts are the types listed: sites, reports, pictures.
var foundExts = map[string]bool{
	".html": true, ".htm": true, ".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
}

// foundRoot is a folder agents keep their working files in, and whose they are; projects says
// its first folders are named by a project's path (Claude Code's are).
type foundRoot struct {
	dir, agent string
	projects   bool
}

// codexImages is where Codex keeps the pictures it generated: generated_images in its home.
func codexImages() string {
	home := os.Getenv("CODEX_HOME")
	if !filepath.IsAbs(home) {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(h, ".codex")
	}
	p := filepath.Join(home, "generated_images")
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p
}

// foundRoots are the folders looked in; a test puts its own.
var foundRoots = func() []foundRoot {
	uid := strconv.Itoa(os.Getuid())
	roots := []foundRoot{
		{"/private/tmp/claude-" + uid, "agent:claude", true},
		{"/private/tmp/codex-daemon-" + uid, "agent:codex", false},
	}
	if p := codexImages(); p != "" {
		roots = append(roots, foundRoot{p, "agent:codex", false})
	}
	return roots
}

// FoundArtifacts lists the pages, reports and pictures in the agents' working folders of this
// Mac, newest first, whose name or address has match in it. A folder that is not there, or
// cannot be read, has none.
func (n *Node) FoundArtifacts(match string) ([]store.Artifact, error) {
	match = strings.ToLower(strings.TrimSpace(match))
	var out []store.Artifact
	visits, until := 0, time.Now().Add(foundTime)
	for _, r := range foundRoots() {
		root := filepath.Clean(r.dir)
		if st, err := os.Lstat(root); err != nil || !st.IsDir() {
			continue // not there, or a link put in its place
		}
		site := "" // the folder of a site being walked: only its index.html is listed
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if visits++; visits > foundVisits || (visits%512 == 0 && time.Now().After(until)) {
				return filepath.SkipAll
			}
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if p == root {
					return nil
				}
				if strings.HasPrefix(name, ".") || name == "node_modules" || strings.Count(p[len(root):], string(filepath.Separator)) > foundDepth {
					return filepath.SkipDir
				}
				if site != "" && !within(p, site) {
					site = ""
				}
				if site == "" {
					if st, err := os.Lstat(filepath.Join(p, "index.html")); err == nil && st.Mode().IsRegular() {
						site = p
					}
				}
				return nil
			}
			if !d.Type().IsRegular() || strings.HasPrefix(name, ".") || !foundExts[strings.ToLower(filepath.Ext(name))] {
				return nil
			}
			dir := filepath.Dir(p)
			if site != "" && !within(dir, site) {
				site = ""
			}
			shown := name
			if site != "" {
				if dir != site || name != "index.html" {
					return nil
				}
				shown = filepath.Base(site) + "/index.html"
			}
			if match != "" && !strings.Contains(strings.ToLower(p), match) {
				return nil
			}
			st, err := d.Info()
			if err != nil || st.Size() == 0 {
				return nil
			}
			out = append(out, store.Artifact{
				Kind: "change", Name: shown, Type: typeOf(name, "", nil), Size: st.Size(), Path: p,
				Agent: r.agent, At: st.ModTime().UTC(),
			})
			if r.projects {
				out[len(out)-1].Title = foundProject(root, p)
			}
			return nil
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > foundMax {
		out = out[:foundMax]
	}
	if out == nil {
		out = []store.Artifact{}
	}
	return out, nil
}

// within is whether p is dir or under it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// foundProject names the project a file was made for: the first folder under the root, which
// Claude Code names by the project's path with dashes for slashes ("-Users-me-site" → "site").
func foundProject(root, p string) string {
	rel := strings.TrimPrefix(p, root+string(filepath.Separator))
	first, _, ok := strings.Cut(rel, string(filepath.Separator))
	if !ok {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil {
		slug := strings.ReplaceAll(home, string(filepath.Separator), "-")
		if first == slug {
			return "~"
		}
		if rest, ok := strings.CutPrefix(first, slug+"-"); ok {
			return rest
		}
	}
	return strings.TrimLeft(first, "-")
}

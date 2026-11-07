//go:build darwin

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Finding a task's directory as it is typed: a path is completed from its parent's directories;
// a word is looked for among the directories of home (three levels down, hidden ones and
// dependency trees left out), indexed once a minute at most.

var dirIndex struct {
	sync.Mutex
	at   time.Time
	dirs []string
}

var skipDirs = map[string]bool{"node_modules": true, "Library": true, "vendor": true, "target": true, "dist": true, "build": true, "__pycache__": true, "Applications": true, "Pictures": true, "Music": true, "Movies": true}

func homeDirs(home string) []string {
	dirIndex.Lock()
	defer dirIndex.Unlock()
	if time.Since(dirIndex.at) < time.Minute && dirIndex.dirs != nil {
		return dirIndex.dirs
	}
	var out []string
	deadline := time.Now().Add(2 * time.Second)
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p == home {
			return nil
		}
		name := d.Name()
		rel, _ := filepath.Rel(home, p)
		if strings.HasPrefix(name, ".") || skipDirs[name] || time.Now().After(deadline) || len(out) > 50000 {
			return filepath.SkipDir
		}
		out = append(out, p)
		if strings.Count(rel, string(filepath.Separator)) >= 2 {
			return filepath.SkipDir
		}
		return nil
	})
	dirIndex.dirs, dirIndex.at = out, time.Now()
	return out
}

// SearchDirs finds directories for what is typed: a path (/…, ~/…) completes, a word searches home.
func (n *Node) SearchDirs(q string) []string {
	q = strings.TrimSpace(q)
	home, _ := os.UserHomeDir()
	if q == "" {
		return []string{}
	}
	if strings.HasPrefix(q, "~") {
		q = home + q[1:]
	}
	var out []string
	if strings.HasPrefix(q, "/") {
		parent, base := filepath.Dir(q), filepath.Base(q)
		if strings.HasSuffix(q, "/") {
			parent, base = filepath.Clean(q), ""
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			return []string{}
		}
		for _, e := range entries {
			name := e.Name()
			isDir := e.IsDir()
			if e.Type()&os.ModeSymlink != 0 {
				if fi, err := os.Stat(filepath.Join(parent, name)); err == nil {
					isDir = fi.IsDir()
				}
			}
			if !isDir || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) {
				continue
			}
			if strings.HasPrefix(strings.ToLower(name), strings.ToLower(base)) {
				out = append(out, filepath.Join(parent, name))
			}
			if len(out) >= 40 {
				break
			}
		}
		return out
	}
	lq := strings.ToLower(q)
	type hit struct {
		p     string
		score int
	}
	var hits []hit
	for _, d := range homeDirs(home) {
		name := strings.ToLower(filepath.Base(d))
		switch {
		case name == lq:
			hits = append(hits, hit{d, 0})
		case strings.HasPrefix(name, lq):
			hits = append(hits, hit{d, 1})
		case strings.Contains(name, lq):
			hits = append(hits, hit{d, 2})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return len(hits[i].p) < len(hits[j].p)
	})
	for _, h := range hits[:min(len(hits), 40)] {
		out = append(out, h.p)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

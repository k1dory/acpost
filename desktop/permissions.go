//go:build darwin

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// /permissions, as Claude Code has it: the rules that let a tool run without asking (allow), make
// it ask (ask) or refuse it (deny), and the directories besides the folder a session may reach
// (Workspace). Claude Code keeps them in its settings files — the person's, the project's (shared,
// committed), the project's local one (this Mac's, ignored by git) — and reads them as a session
// starts; the ACP agent the node runs is Claude Code, and reads the same files. Its terminal draws
// /permissions itself, so through ACP there is none: the app edits the files.

// PermSource is one settings file and the rules it holds.
type PermSource struct {
	Source string   `json:"source"` // user, project, local
	Path   string   `json:"path"`
	Exists bool     `json:"exists"`
	Allow  []string `json:"allow"`
	Ask    []string `json:"ask"`
	Deny   []string `json:"deny"`
	Dirs   []string `json:"dirs"`            // additionalDirectories
	Mode   string   `json:"mode,omitempty"`  // defaultMode
	Error  string   `json:"error,omitempty"` // the file is there but not read (not JSON): not written either
}

// permLists are the lists of "permissions" an edit may change, by the name the app gives them.
var permLists = map[string]string{"allow": "allow", "ask": "ask", "deny": "deny", "dirs": "additionalDirectories"}

// permPaths are the settings files for a folder, in the order Claude Code ranks them (the later
// win): the person's, the project's, the project's local one. With no folder (or the home folder,
// whose .claude is the person's own): the person's alone, and the local one of home.
func permPaths(folder string) ([][2]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	out := [][2]string{{"user", filepath.Join(home, ".claude", "settings.json")}}
	if folder == "" {
		folder = home
	}
	folder = filepath.Clean(expandHome(folder))
	if !filepath.IsAbs(folder) {
		return nil, errors.New("a folder's path is absolute")
	}
	if folder != home {
		out = append(out, [2]string{"project", filepath.Join(folder, ".claude", "settings.json")})
	}
	return append(out, [2]string{"local", filepath.Join(folder, ".claude", "settings.local.json")}), nil
}

// PermissionRules reads the rules of the settings files that apply in folder.
func (n *Node) PermissionRules(folder string) ([]PermSource, error) {
	paths, err := permPaths(folder)
	if err != nil {
		return nil, err
	}
	out := make([]PermSource, 0, len(paths))
	for _, p := range paths {
		s := PermSource{Source: p[0], Path: p[1], Allow: []string{}, Ask: []string{}, Deny: []string{}, Dirs: []string{}}
		b, err := os.ReadFile(p[1])
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			s.Error = err.Error()
		default:
			s.Exists = true
			var f struct {
				Permissions struct {
					Allow, Ask, Deny      []string
					AdditionalDirectories []string `json:"additionalDirectories"`
					DefaultMode           string   `json:"defaultMode"`
				} `json:"permissions"`
			}
			if len(bytes.TrimSpace(b)) > 0 {
				if err := json.Unmarshal(b, &f); err != nil {
					s.Error = fmt.Sprintf("not read: %v", err)
					break
				}
			}
			pm := f.Permissions
			s.Allow, s.Ask, s.Deny, s.Dirs, s.Mode = orEmpty(pm.Allow), orEmpty(pm.Ask), orEmpty(pm.Deny), orEmpty(pm.AdditionalDirectories), pm.DefaultMode
		}
		out = append(out, s)
	}
	return out, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// PermissionEdit adds rule to a list of a settings file (or removes it: add false), the rest of the
// file kept as it was — its other settings, their order. A file that is not JSON is not touched.
func (n *Node) PermissionEdit(folder, source, list, rule string, add bool) error {
	key, ok := permLists[list]
	if !ok {
		return fmt.Errorf("no list %q: allow, ask, deny or dirs", list)
	}
	rule = strings.TrimSpace(rule)
	if rule == "" || len(rule) > 2000 || strings.ContainsAny(rule, "\n\r") {
		return errors.New("a rule is one line of text")
	}
	paths, err := permPaths(folder)
	if err != nil {
		return err
	}
	path := ""
	for _, p := range paths {
		if p[0] == source {
			path = p[1]
		}
	}
	if path == "" {
		return fmt.Errorf("no settings %q here", source)
	}
	return editPermList(path, key, rule, add)
}

// permMu keeps the app's own edits one at a time: two rules added at once both stay.
var permMu sync.Mutex

// editPermList edits the file as it is when written: read, changed, and written only if the file
// is still what was read (Claude Code may write its settings meanwhile); else read again, a few times.
func editPermList(path, key, rule string, add bool) error {
	permMu.Lock()
	defer permMu.Unlock()
	for range 5 {
		err := editPermOnce(path, key, rule, add)
		if !errors.Is(err, errPermChanged) {
			return err
		}
	}
	return fmt.Errorf("%s kept changing while it was edited: not written", path)
}

// permBeforeWrite, in tests, runs at the check before the write takes the file's place.
var permBeforeWrite func()

var errPermChanged = errors.New("the settings file changed while it was edited")

func editPermOnce(path, key, rule string, add bool) error {
	if _, err := os.Stat(filepath.Dir(path)); errors.Is(err, fs.ErrNotExist) {
		if !add {
			return nil // no .claude: nothing to take out
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
	}
	path, err := writeTarget(path) // a link (dotfiles kept elsewhere) is written through
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	var keep *os.FileMode
	existed := err == nil
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !add {
			return nil // nothing there: nothing to take out
		}
		b = nil
	case err != nil:
		return err
	default:
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		m := st.Mode().Perm()
		keep = &m
	}
	top, err := readObject(b)
	if err != nil {
		return fmt.Errorf("%s is not read as JSON (%v): left as it is", path, err)
	}
	perm, err := readObject(top.get("permissions"))
	if err != nil {
		return fmt.Errorf("%s: its permissions are not an object: left as it is", path)
	}
	var cur []string
	if raw := perm.get(key); raw != nil {
		if err := json.Unmarshal(raw, &cur); err != nil {
			return fmt.Errorf("%s: permissions.%s is not a list of text: left as it is", path, key)
		}
	}
	has := slices.Contains(cur, rule)
	switch {
	case add && has, !add && !has:
		return nil // already as asked
	case add:
		cur = append(cur, rule)
	default:
		cur = slices.DeleteFunc(cur, func(s string) bool { return s == rule })
	}
	if len(cur) == 0 {
		perm.del(key)
	} else {
		v, _ := json.Marshal(cur)
		perm.set(key, v)
	}
	if len(perm.keys) == 0 {
		top.del("permissions")
	} else {
		top.set("permissions", perm.bytes())
	}
	var out bytes.Buffer
	if err := json.Indent(&out, top.bytes(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	// Written only if the file is still what was read (or still none), checked last — the new
	// content already beside it, just before it takes the file's place; else it is read and edited
	// again. Claude Code writes its settings without a lock to share: a write of its own in the
	// instant between that check and the rename is the one case this cannot see.
	same := func() error {
		if permBeforeWrite != nil {
			permBeforeWrite()
		}
		now, err := os.ReadFile(path)
		switch {
		case !existed && errors.Is(err, fs.ErrNotExist):
			return nil
		case !existed && err == nil, existed && err == nil && !bytes.Equal(now, b), existed && errors.Is(err, fs.ErrNotExist):
			return errPermChanged
		case err != nil:
			return err
		}
		return nil
	}
	err = writeAtomicIf(path, out.Bytes(), 0o644, keep, existed, same)
	if !existed && errors.Is(err, fs.ErrExist) {
		return errPermChanged // made meanwhile: linked in only where there is none
	}
	return err
}

// object is a JSON object whose members keep their order and their text.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

// readObject reads an object (nothing, or null: an empty one).
func readObject(b []byte) (*object, error) {
	o := &object{vals: map[string]json.RawMessage{}}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return o, nil
	}
	d := json.NewDecoder(bytes.NewReader(b))
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		k, _ := tok.(string)
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := o.vals[k]; !dup {
			o.keys = append(o.keys, k)
		}
		o.vals[k] = v
	}
	if _, err := d.Token(); err != nil { // the closing brace
		return nil, err
	}
	// Nothing may follow it (a comment, a second object): such a file is not JSON, and is left whole.
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more after the object")
	}
	return o, nil
}

func (o *object) get(k string) json.RawMessage { return o.vals[k] }

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	o.keys = slices.DeleteFunc(o.keys, func(s string) bool { return s == k })
}

func (o *object) bytes() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

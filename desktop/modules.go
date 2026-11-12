//go:build darwin

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/k1dory/acpost/desktop/preview"
	"github.com/k1dory/acpost/internal/config"
)

// Modules: views the person adds to the app beside "Agents" and "IDE" — a web page (a tracker, a
// wiki, a dashboard, a tool served on this Mac) in a native pane of the window, the chat beside
// it, as the IDE is. A module is a name and an address; its page is a page like any in a browser:
// nothing of the app's (its node, its calls) is within its reach. They are kept in modules.json
// beside the node's configuration.

// Module is one of them.
type Module struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

const (
	maxModules    = 24
	maxModuleName = 40
	maxModuleURL  = 2000
)

var modulesMu sync.Mutex // one reader or writer of the file at a time; the panes' calls in order

// modulesPath is the file (a variable: tests keep theirs).
var modulesPath = func() string { return filepath.Join(filepath.Dir(config.DefaultPath()), "modules.json") }

func readModules() ([]Module, error) {
	b, err := os.ReadFile(modulesPath())
	if os.IsNotExist(err) {
		return []Module{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ms []Module
	if err := json.Unmarshal(b, &ms); err != nil {
		return nil, fmt.Errorf("%s: %w (fix it, or remove it)", modulesPath(), err)
	}
	if ms == nil {
		ms = []Module{}
	}
	return ms, nil
}

func writeModules(ms []Module) error {
	b, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return err
	}
	path := modulesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'), 0o600, nil, true)
}

// checkModule gives m as it is kept: the name trimmed, the address an http or https one, whole.
func checkModule(m Module) (Module, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.URL = strings.TrimSpace(m.URL)
	switch {
	case m.Name == "":
		return m, errors.New("a module has a name")
	case utf8.RuneCountInString(m.Name) > maxModuleName || strings.ContainsFunc(m.Name, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return m, fmt.Errorf("a module's name: up to %d printable characters", maxModuleName)
	case len(m.URL) > maxModuleURL:
		return m, fmt.Errorf("a module's address: up to %d characters", maxModuleURL)
	}
	if !strings.Contains(m.URL, "://") {
		m.URL = "https://" + m.URL // "wiki.example.com" as typed
	}
	u, err := url.Parse(m.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return m, errors.New("a module's address is an http or https one, such as https://wiki.example.com or http://localhost:3000")
	}
	if u.User != nil {
		return m, errors.New("a module's address has no user or password in it: sign in on its page")
	}
	m.URL = u.String()
	return m, nil
}

// Modules lists them, in the order they were added.
func (n *Node) Modules() ([]Module, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	return readModules()
}

// PutModule adds a module (no id) or changes one: the module as kept.
func (n *Node) PutModule(m Module) (Module, error) {
	m, err := checkModule(m)
	if err != nil {
		return m, err
	}
	modulesMu.Lock()
	defer modulesMu.Unlock()
	ms, err := readModules()
	if err != nil {
		return m, err
	}
	for i := range ms {
		if ms[i].ID != m.ID && strings.EqualFold(ms[i].Name, m.Name) {
			return m, fmt.Errorf("there is a module named %s already", ms[i].Name)
		}
	}
	at := -1
	for i := range ms {
		if m.ID != "" && ms[i].ID == m.ID {
			at = i
		}
	}
	switch {
	case at >= 0:
		ms[at] = m
	case m.ID != "":
		return m, errors.New("that module is no longer there")
	case len(ms) >= maxModules:
		return m, fmt.Errorf("up to %d modules", maxModules)
	default:
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return m, err
		}
		m.ID = "mod_" + hex.EncodeToString(b[:])
		ms = append(ms, m)
	}
	return m, writeModules(ms)
}

// DeleteModule removes a module, and its pane with its page.
func (n *Node) DeleteModule(id string) error {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	ms, err := readModules()
	if err != nil {
		return err
	}
	out := ms[:0]
	for _, m := range ms {
		if m.ID != id {
			out = append(out, m)
		}
	}
	if len(out) == len(ms) {
		return nil
	}
	if err := writeModules(out); err != nil {
		return err
	}
	forgetModule(id)
	return nil
}

// The panes' native calls (variables: tests have no window).
var (
	showModule = func(n *Node, m Module, x, y, w, h float64, dark bool) error {
		if n.win == nil {
			return errPreview
		}
		return preview.ModuleShow(n.win.NativeWindow(), m.ID, m.URL, x, y, w, h, dark)
	}
	forgetModule = preview.ModuleForget
)

// ModuleShow lays module id's pane over the box at x, y, w × h (window points), opened at the
// address kept for it (the interface names the module, never an address).
func (n *Node) ModuleShow(id string, x, y, w, h float64, dark bool) error {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	ms, err := readModules()
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.ID == id {
			return showModule(n, m, x, y, w, h, dark)
		}
	}
	return errors.New("that module is no longer there")
}

// ModuleHide hides the modules' panes; their pages stay as they are.
func (n *Node) ModuleHide() {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	preview.ModuleHide()
}

// ModuleReload opens module id at its own address again.
func (n *Node) ModuleReload(id string) {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	preview.ModuleReload(id)
}

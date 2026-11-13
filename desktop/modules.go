//go:build darwin

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/k1dory/acpost/desktop/preview"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/plat"
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

// lockModules takes the list for a change, in this app and across the apps of this user that
// share it (the installed one and a build beside it): each reads what the other wrote, none
// writes over it. The lock is a file of its own beside the list (the list is replaced whole).
func lockModules() (unlock func(), err error) {
	modulesMu.Lock()
	path := modulesPath() + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		modulesMu.Unlock()
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		if err = plat.Lock(f, true); err != nil {
			f.Close()
		}
	}
	if err != nil {
		modulesMu.Unlock()
		return nil, err
	}
	return func() { _ = plat.Unlock(f); f.Close(); modulesMu.Unlock() }, nil
}

var moduleEvents sync.Once

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
	// Plain http is for a tool served on this Mac or on the network it is in: the address is
	// localhost or a number of a private network (10.x, 172.16–31.x, 192.168.x, 127.x). The system's
	// transport security is opened to the local network alone (Info.plist), and the module's pane
	// refuses a plain page of any other host.
	if u.Scheme == "http" && !localHost(u.Hostname()) {
		return m, errors.New("a module on the internet is an https address; plain http is for this Mac and its network (http://localhost:3000, http://192.168.1.10:8080)")
	}
	if u.User != nil {
		return m, errors.New("a module's address has no user or password in it: sign in on its page")
	}
	m.URL = u.String()
	return m, nil
}

// localHost is whether host is this Mac or a numbered address of a private network: the hosts a
// module may be read from over plain http. A name other than localhost is not one, whatever it
// resolves to: what it resolves to is another's to change.
func localHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return a.IsLoopback() || (a.Is4() && a.IsPrivate())
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
	unlock, err := lockModules()
	if err != nil {
		return m, err
	}
	defer unlock()
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
	unlock, err := lockModules()
	if err != nil {
		return err
	}
	defer unlock()
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
		// What becomes of its page (loaded, or not and why) is told to the interface.
		moduleEvents.Do(func() {
			preview.OnModuleEvent(func(e preview.ModuleEvent) { n.app.Event.Emit("module:event", e) })
		})
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

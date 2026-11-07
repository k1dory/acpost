package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
)

// Tokens are kept in the macOS keychain (service "acpost-forge", account the host; given to
// `security` on its stdin, never on a command line) or, elsewhere, in a file only its owner reads
// (forge-tokens.json, 0600). What kind of forge a host is, and nothing secret, is in forges.json
// beside it.

const keychainService = "acpost-forge"

// Store keeps hosts' kinds and tokens.
type Store struct {
	Dir  string // ~/.config/acpost
	File bool   // the file even on macOS (tests)
	mu   sync.Mutex
}

func (s *Store) keychain() bool { return runtime.GOOS == "darwin" && !s.File }

func (s *Store) kindsFile() string  { return filepath.Join(s.Dir, "forges.json") }
func (s *Store) tokensFile() string { return filepath.Join(s.Dir, "forge-tokens.json") }

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any, mode os.FileMode) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	f, err := os.CreateTemp(filepath.Dir(path), ".forge-*") // its own: writers do not share one
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// locked runs fn holding the store's lock, across processes (an acpost login and a node).
func (s *Store) locked(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, ".forge.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// security runs the macOS security(1) tool outside any terminal: it reads a password from its
// controlling terminal when it has one, and from stdin (what it is given here) only when not.
func security(stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command("security", args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.CombinedOutput()
}

// Kind is the kind kept for a host ("" when none).
func (s *Store) Kind(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	kinds := map[string]string{}
	_ = readJSON(s.kindsFile(), &kinds)
	return kinds[host]
}

// Login keeps a host's token (and kind, when given).
func (s *Store) Login(host, kind, token string) error {
	host, token = strings.TrimSpace(host), strings.TrimSpace(token)
	if host == "" || token == "" || strings.ContainsAny(host, "/ \t\n") {
		return fmt.Errorf("a host (no scheme, no path) and a token")
	}
	return s.locked(func() error {
		if kind != "" {
			kinds := map[string]string{}
			if err := readJSON(s.kindsFile(), &kinds); err != nil {
				return err
			}
			kinds[host] = kind
			if err := writeJSON(s.kindsFile(), kinds, 0o600); err != nil {
				return err
			}
		}
		if s.keychain() {
			// Asked twice: given twice, on stdin (never on a command line).
			if out, err := security(token+"\n"+token+"\n", "add-generic-password", "-U", "-s", keychainService, "-a", host, "-w"); err != nil {
				return fmt.Errorf("keychain: %v %s", err, bytes.TrimSpace(out))
			}
			return nil
		}
		tokens := map[string]string{}
		if err := readJSON(s.tokensFile(), &tokens); err != nil {
			return err
		}
		tokens[host] = token
		return writeJSON(s.tokensFile(), tokens, 0o600)
	})
}

// Logout forgets a host's token.
func (s *Store) Logout(host string) error {
	return s.locked(func() error {
		if s.keychain() {
			_, _ = security("", "delete-generic-password", "-s", keychainService, "-a", host)
			return nil
		}
		tokens := map[string]string{}
		if err := readJSON(s.tokensFile(), &tokens); err != nil {
			return err
		}
		delete(tokens, host)
		return writeJSON(s.tokensFile(), tokens, 0o600)
	})
}

// Token is a host's token ("" when none is kept).
func (s *Store) Token(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keychain() {
		cmd := exec.Command("security", "find-generic-password", "-s", keychainService, "-a", host, "-w")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	tokens := map[string]string{}
	_ = readJSON(s.tokensFile(), &tokens)
	return tokens[host]
}

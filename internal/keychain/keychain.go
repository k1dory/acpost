// Package keychain keeps secrets: the macOS keychain (security(1), values on stdin, never on a
// command line), or a 0600 file elsewhere (and in tests).
package keychain

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

// Store keeps secrets under a service name, by account.
type Store struct {
	Service string
	Dir     string // for the file
	File    bool   // the file even on macOS
	mu      sync.Mutex
}

// Default is the store of a service for this process: the keychain on macOS unless
// ACPOST_SECRETS_FILE=1, a file in dir otherwise.
func Default(service, dir string) *Store {
	return &Store{Service: service, Dir: dir, File: runtime.GOOS != "darwin" || os.Getenv("ACPOST_SECRETS_FILE") == "1"}
}

func (s *Store) file() string { return filepath.Join(s.Dir, s.Service+".secrets.json") }

func (s *Store) read() (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(s.file())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(b, &out)
}

func (s *Store) write(m map[string]string) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(m)
	tmp := s.file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file())
}

func security(stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command("security", args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // read from stdin, never a terminal
	return cmd.CombinedOutput()
}

// Set keeps a secret.
func (s *Store) Set(account, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.File {
		if out, err := security(value+"\n"+value+"\n", "add-generic-password", "-U", "-s", s.Service, "-a", account, "-w"); err != nil {
			return fmt.Errorf("keychain: %v %s", err, bytes.TrimSpace(out))
		}
		return nil
	}
	m, err := s.read()
	if err != nil {
		return err
	}
	m[account] = value
	return s.write(m)
}

// Get is a secret ("" when none).
func (s *Store) Get(account string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.File {
		cmd := exec.Command("security", "find-generic-password", "-s", s.Service, "-a", account, "-w")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimRight(string(out), "\n")
	}
	m, _ := s.read()
	return m[account]
}

// Delete forgets a secret.
func (s *Store) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.File {
		_, _ = security("", "delete-generic-password", "-s", s.Service, "-a", account)
		return nil
	}
	m, err := s.read()
	if err != nil {
		return err
	}
	delete(m, account)
	return s.write(m)
}

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

	"github.com/k1dory/acpost/internal/plat"
)

// Store keeps secrets under a service name, by account.
type Store struct {
	Service string
	Dir     string // for the file
	File    bool   // the file even on macOS
	// FileName is the file's name in Dir ("" : <service>.secrets.json).
	FileName string
	mu       sync.Mutex
}

// Default is the store of a service for this process: the keychain on macOS unless
// ACPOST_SECRETS_FILE=1, a file in dir otherwise.
func Default(service, dir string) *Store {
	return &Store{Service: service, Dir: dir, File: runtime.GOOS != "darwin" || os.Getenv("ACPOST_SECRETS_FILE") == "1"}
}

func (s *Store) file() string {
	if s.FileName != "" {
		return filepath.Join(s.Dir, s.FileName)
	}
	return filepath.Join(s.Dir, s.Service+".secrets.json")
}

func (s *Store) read() (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(s.file())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(b, &out) != nil {
		// Not json's own words: they quote what the file holds.
		return nil, fmt.Errorf("%s: not a secrets file", s.file())
	}
	return out, nil
}

func (s *Store) write(m map[string]string) error {
	b, _ := json.Marshal(m)
	f, err := plat.CreatePrivateTemp(s.Dir, s.Service+".secrets.")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.file())
}

// locked runs fn holding the file's lock (other processes of the same home too).
func (s *Store) locked(fn func() error) error {
	if err := plat.MkdirPrivate(s.Dir); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(s.Dir, "."+s.Service+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := plat.Lock(lf, true); err != nil {
		return err
	}
	defer plat.Unlock(lf)
	return fn()
}

func security(stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command("security", args...)
	cmd.Stdin = strings.NewReader(stdin)
	plat.Detach(cmd) // read from stdin, never a terminal
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
	return s.locked(func() error {
		m, err := s.read()
		if err != nil {
			return err
		}
		m[account] = value
		return s.write(m)
	})
}

// Lookup is a secret, whether one is kept, and an error when the store could not be read (a
// locked keychain, a headless session, a broken file): never "" in place of a secret not read.
func (s *Store) Lookup(account string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.File {
		cmd := exec.Command("security", "find-generic-password", "-s", s.Service, "-a", account, "-w")
		plat.Detach(cmd)
		out, err := cmd.Output()
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 { // errSecItemNotFound
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("keychain: %v", err)
		}
		return strings.TrimRight(string(out), "\n"), true, nil
	}
	m, err := s.read()
	if err != nil {
		return "", false, err
	}
	v, ok := m[account]
	return v, ok, nil
}

// Delete forgets a secret.
func (s *Store) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.File {
		out, err := security("", "delete-generic-password", "-s", s.Service, "-a", account)
		var ee *exec.ExitError
		if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 44) {
			return fmt.Errorf("keychain: %v %s", err, bytes.TrimSpace(out))
		}
		return nil
	}
	return s.locked(func() error {
		m, err := s.read()
		if err != nil {
			return err
		}
		delete(m, account)
		return s.write(m)
	})
}

package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/keychain"
	"github.com/k1dory/acpost/internal/plat"
)

// Tokens are kept by internal/keychain: the macOS keychain (service "acpost-forge", account the
// host) or, elsewhere, a file only its owner reads (forge-tokens.json, 0600, the name it always
// had). What kind of forge a host is, and nothing secret, is in forges.json beside it.

const keychainService = "acpost-forge"

// tokenFor is how long a token read from the keychain is used before it is read again: every
// request to a forge asks for one, and each read there is a process (security(1)).
const tokenFor = time.Minute

// Store keeps hosts' kinds and tokens.
type Store struct {
	Dir  string // ~/.config/acpost
	File bool   // the file even on macOS (tests)

	once   sync.Once
	kc     *keychain.Store
	mu     sync.Mutex // kinds (with the lock file across processes) and the cache
	cached map[string]cachedToken
}

type cachedToken struct {
	token string
	at    time.Time
}

// DefaultStore is the store of this process: the keychain on macOS unless ACPOST_SECRETS_FILE=1,
// the file in ~/.config/acpost otherwise. The node and acpost forge login use the same one.
func DefaultStore() *Store {
	return &Store{Dir: plat.ConfigDir(), File: os.Getenv("ACPOST_SECRETS_FILE") == "1"}
}

func (s *Store) tokens() *keychain.Store {
	s.once.Do(func() {
		s.kc = &keychain.Store{Service: keychainService, Dir: s.Dir, File: s.File || runtime.GOOS != "darwin", FileName: "forge-tokens.json"}
	})
	return s.kc
}

func (s *Store) kindsFile() string { return filepath.Join(s.Dir, "forges.json") }

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

// writeJSON writes v to path, a file of this user alone, whole or not at all.
func writeJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	f, err := plat.CreatePrivateTemp(filepath.Dir(path), ".forge-") // its own: writers do not share one
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
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
	if err := plat.MkdirPrivate(s.Dir); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, ".forge.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := plat.Lock(f, true); err != nil {
		return err
	}
	defer plat.Unlock(f)
	return fn()
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
		delete(s.cached, host)
		// The host is listed (its kind, when given): the accounts are these hosts.
		kinds := map[string]string{}
		if err := readJSON(s.kindsFile(), &kinds); err != nil {
			return err
		}
		if kind != "" || kinds[host] == "" {
			kinds[host] = kind
		}
		if err := writeJSON(s.kindsFile(), kinds); err != nil {
			return err
		}
		return s.tokens().Set(host, token)
	})
}

// Hosts lists the hosts logged in to (and those given a kind).
func (s *Store) Hosts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	kinds := map[string]string{}
	_ = readJSON(s.kindsFile(), &kinds)
	out := make([]string, 0, len(kinds))
	for h := range kinds {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Logout forgets a host's token, then the host. A token that could not be forgotten is an error
// and the host stays listed: a logout said to be done leaves nothing a request could still use.
func (s *Store) Logout(host string) error {
	return s.locked(func() error {
		delete(s.cached, host)
		if err := s.tokens().Delete(host); err != nil {
			return err
		}
		kinds := map[string]string{}
		if err := readJSON(s.kindsFile(), &kinds); err != nil {
			return err
		}
		if _, ok := kinds[host]; !ok {
			return nil
		}
		delete(kinds, host)
		return writeJSON(s.kindsFile(), kinds)
	})
}

// Token is a host's token ("" when none is kept, or when the store cannot be read). From the
// keychain, a token read is used for tokenFor; Login and Logout of this store forget it at once.
func (s *Store) Token(host string) string {
	kc := s.tokens()
	if kc.File {
		v, _, _ := kc.Lookup(host)
		return strings.TrimSpace(v)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cached[host]; ok && time.Since(c.at) < tokenFor {
		return c.token
	}
	v, _, err := kc.Lookup(host)
	if err != nil {
		return "" // not kept: a locked keychain is asked again next time
	}
	if s.cached == nil {
		s.cached = map[string]cachedToken{}
	}
	s.cached[host] = cachedToken{token: strings.TrimSpace(v), at: time.Now()}
	return strings.TrimSpace(v)
}

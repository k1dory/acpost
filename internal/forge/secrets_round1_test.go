package forge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeKeychain puts a security(1) in PATH that keeps items as files in a directory, counts its
// find-generic-password calls, and refuses deletes while fail-delete exists there.
func fakeKeychain(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the keychain is macOS's")
	}
	d := t.TempDir()
	script := `#!/bin/sh
d='` + d + `'
cmd=$1; shift
acct=""
while [ $# -gt 0 ]; do [ "$1" = "-a" ] && acct=$2; shift; done
case $cmd in
add-generic-password) read -r v; printf '%s' "$v" > "$d/item-$acct";;
find-generic-password) echo x >> "$d/finds"; [ -f "$d/item-$acct" ] || exit 44; cat "$d/item-$acct"; echo;;
delete-generic-password) [ -f "$d/fail-delete" ] && { echo "User interaction is not allowed." >&2; exit 36; }; [ -f "$d/item-$acct" ] || exit 44; rm "$d/item-$acct";;
esac
`
	if err := os.WriteFile(filepath.Join(d, "security"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d+string(os.PathListSeparator)+os.Getenv("PATH"))
	return d
}

func finds(d string) int {
	b, _ := os.ReadFile(filepath.Join(d, "finds"))
	return strings.Count(string(b), "x")
}

// A keychain that refuses to forget the token is a failed logout: the host stays listed, its
// token still there, and the error says so (it was reported as done).
func TestLogoutThatFailsSaysSo(t *testing.T) {
	kd := fakeKeychain(t)
	s := &Store{Dir: t.TempDir()}
	if err := s.Login("git.example", "forgejo", "tok"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(kd, "fail-delete"), nil, 0o600)
	if err := s.Logout("git.example"); err == nil {
		t.Fatal("a logout the keychain refused was reported as done")
	}
	if !slices.Contains(s.Hosts(), "git.example") || s.Token("git.example") != "tok" {
		t.Fatalf("after a failed logout: hosts %v, token %q", s.Hosts(), s.Token("git.example"))
	}
	os.Remove(filepath.Join(kd, "fail-delete"))
	if err := s.Logout("git.example"); err != nil {
		t.Fatal(err)
	}
	if len(s.Hosts()) != 0 || s.Token("git.example") != "" {
		t.Fatalf("after logout: hosts %v, token %q", s.Hosts(), s.Token("git.example"))
	}
}

// A token is read from the keychain once, not on every request (each read is a process); a login
// of the same store is seen at once.
func TestTokenIsReadOnce(t *testing.T) {
	kd := fakeKeychain(t)
	s := &Store{Dir: t.TempDir()}
	if err := s.Login("git.example", "", "one"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Token("git.example") != "one" {
				t.Error("token")
			}
		}()
	}
	wg.Wait()
	if n := finds(kd); n != 1 {
		t.Fatalf("security ran %d times for 8 requests", n)
	}
	if err := s.Login("git.example", "", "two"); err != nil {
		t.Fatal(err)
	}
	if got := s.Token("git.example"); got != "two" {
		t.Fatalf("after a new login: %q", got)
	}
}

// Tokens kept by an earlier acpost (forge-tokens.json, indented) are read as they are.
func TestOldTokensFileIsRead(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.MarshalIndent(map[string]string{"git.example": "old-tok"}, "", "  ")
	os.WriteFile(filepath.Join(dir, "forge-tokens.json"), b, 0o600)
	os.WriteFile(filepath.Join(dir, "forges.json"), []byte(`{"git.example": "forgejo"}`), 0o600)
	s := &Store{Dir: dir, File: true}
	if s.Token("git.example") != "old-tok" || s.Kind("git.example") != "forgejo" {
		t.Fatalf("token %q kind %q", s.Token("git.example"), s.Kind("git.example"))
	}
	if err := s.Logout("git.example"); err != nil || s.Token("git.example") != "" || len(s.Hosts()) != 0 {
		t.Fatalf("logout: %v", err)
	}
}

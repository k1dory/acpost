package keychain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSecurity puts a security(1) in PATH that exits with code (printing out first).
func fakeSecurity(t *testing.T, out string, code int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' '" + out + "'\nexit " + itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// A keychain that cannot be read (locked, no GUI session: security exits non-zero, not 44) is an
// error, not an empty secret; an item not there is "not kept".
func TestLookupTellsUnreadFromMissing(t *testing.T) {
	s := &Store{Service: "acpost-test", Dir: t.TempDir()}
	fakeSecurity(t, "", 51)
	if v, ok, err := s.Lookup("a"); err == nil || ok || v != "" {
		t.Fatalf("a locked keychain: %q %v %v", v, ok, err)
	}
	fakeSecurity(t, "", 44)
	if v, ok, err := s.Lookup("a"); err != nil || ok || v != "" {
		t.Fatalf("an item not there: %q %v %v", v, ok, err)
	}
	fakeSecurity(t, "v4lue\n", 0)
	if v, ok, err := s.Lookup("a"); err != nil || !ok || v != "v4lue" {
		t.Fatalf("an item there: %q %v %v", v, ok, err)
	}
}

// A broken secrets file is an error that quotes nothing of what the file holds.
func TestBrokenFileIsAnError(t *testing.T) {
	s := &Store{Service: "acpost-test", Dir: t.TempDir(), File: true}
	if err := os.WriteFile(s.file(), []byte(`{"a":"s3cret`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Lookup("a")
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("%v", err)
	}
}

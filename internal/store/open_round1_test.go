package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A store that fails to open (here: not a database) releases its lock: opening it again fails
// the same way, not with ErrLocked, and once the file is fixed it opens.
func TestFailedOpenReleasesTheLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "store.db")
	garbage := []byte("this is not a sqlite file, long enough to have a header ......................")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err1 := Open("sqlite://"+path, Options{})
	_, err2 := Open("sqlite://"+path, Options{})
	if err1 == nil || err2 == nil {
		t.Fatalf("opened garbage: %v %v", err1, err2)
	}
	if errors.Is(err2, ErrLocked) || err1.Error() != err2.Error() {
		t.Fatalf("first open: %v; second: %v", err1, err2)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatalf("after the file is gone: %v", err)
	}
	s.Close()
}

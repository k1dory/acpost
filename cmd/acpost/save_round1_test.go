package main

import (
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

// A save that cannot be written whole (here the file size limit; a full disk alike) leaves no cut
// file under the name, which a next run would refuse as existing; nor a temporary one.
func TestSaveCutShortLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	signal.Ignore(syscall.SIGXFSZ)
	defer signal.Reset(syscall.SIGXFSZ)
	var old syscall.Rlimit
	syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 1024, Max: old.Max}); err != nil {
		t.Skip(err)
	}
	err = writeIn(root, "big.bin", make([]byte, 64<<10), false)
	syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old)
	if err == nil {
		t.Fatal("a write past the limit succeeded")
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	// Whole: written, and a second save of the name is refused with the first kept.
	if err := writeIn(root, "a.txt", []byte("one"), false); err != nil {
		t.Fatal(err)
	}
	if err := writeIn(root, "a.txt", []byte("two"), false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("a second save: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one" {
		t.Fatalf("%q", b)
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Fatalf("left behind: %v", left)
	}
}

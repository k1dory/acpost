//go:build darwin

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Saving over a file replaces it whole: a reader meanwhile sees the old content or the new,
// never a truncated one (os.WriteFile truncates first; a failure there lost the old file).
func TestSaveReplacesWhole(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "report.bin")
	const size = 1 << 20
	a, b := bytes.Repeat([]byte("a"), size), bytes.Repeat([]byte("b"), size)
	if err := saveFile(dest, a); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var torn atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			got, err := os.ReadFile(dest)
			if err != nil || len(got) != size || !bytes.Equal(got, a) && !bytes.Equal(got, b) {
				torn.Add(1)
			}
		}
	}()
	for i := range 100 {
		data := a
		if i%2 == 0 {
			data = b
		}
		if err := saveFile(dest, data); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if n := torn.Load(); n > 0 {
		t.Fatalf("%d reads saw a partial or missing file", n)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".*")); len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

// The app writes its default configuration only where there is none: one that appeared
// meanwhile (the user's, or another start's) is kept, and nothing is left half-written.
func TestDefaultConfigKeepsAnExistingOne(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "claude-agent-acp"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, "acpost", "config.yaml")
	if err := writeDefaultConfig(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "claude-agent-acp") {
		t.Fatalf("written: %s", b)
	}
	os.WriteFile(path, []byte("mine\n"), 0o600)
	if err := writeDefaultConfig(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "mine\n" {
		t.Fatalf("an existing configuration was replaced: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

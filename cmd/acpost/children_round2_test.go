package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A race a child reported only in its log (a node's serve.log, an autostarted node's acpost.log)
// is found; a clean log is not one.
func TestChildRaceReportsAreFound(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "s"), 0o700)
	os.WriteFile(filepath.Join(dir, "serve.log"), []byte("level=INFO msg=listening\n"), 0o600)
	if p := childProblems(dir); len(p) != 0 {
		t.Fatalf("a clean log: %v", p)
	}
	os.WriteFile(filepath.Join(dir, "s", "acpost.log"), []byte("==================\nWARNING: DATA RACE\nWrite at 0x00c0001\n"), 0o600)
	if p := childProblems(dir); len(p) != 1 || !strings.Contains(p[0], "acpost.log") {
		t.Fatalf("a race in an autostarted node's log: %v", p)
	}
}

// A node a command started in the background is stopped when its test ends (it said nothing of a
// race before it exited), not left running.
func TestAutostartedNodeIsStoppedAtTheEnd(t *testing.T) {
	var pid []byte
	t.Run("inner", func(t *testing.T) {
		e := newEnv(t, inbox)
		e.ok("", "status") // starts one
		var err error
		if pid, err = os.ReadFile(filepath.Join(filepath.Dir(e.sock), "acpost.pid")); err != nil {
			t.Fatal(err)
		}
	})
	if out, _ := exec.Command("ps", "-o", "command=", "-p", strings.TrimSpace(string(pid))).Output(); strings.Contains(string(out), "serve") {
		t.Fatalf("still running after its test: %s", out)
	}
}

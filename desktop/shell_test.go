//go:build darwin

package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellRunsInTheDirectory(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	r, err := runShell("", "pwd; echo err >&2; exit 3", dir, time.Minute)
	if err != nil || r.Code != 3 || !strings.Contains(r.Output, dir) || !strings.Contains(r.Output, "err") {
		t.Fatalf("%+v %v", r, err)
	}
	if r, err := runShell("", "echo ok", filepath.Join(dir, "none"), time.Minute); err != nil || !strings.Contains(r.Output, "ok") {
		t.Fatalf("a directory that is not there: %+v %v", r, err)
	}
	if _, err := runShell("", "  ", dir, time.Minute); err == nil {
		t.Fatal("nothing was run")
	}
}

func TestShellEndsWhatRunsTooLong(t *testing.T) {
	start := time.Now()
	r, err := runShell("", "echo first; sleep 30 & sleep 30", "", 300*time.Millisecond)
	if err != nil || !r.TimedOut || r.Code != -1 || !strings.Contains(r.Output, "first") || time.Since(start) > 10*time.Second {
		t.Fatalf("%+v %v in %v", r, err, time.Since(start))
	}
}

func TestShellStop(t *testing.T) {
	n := &Node{}
	done := make(chan ShellResult, 1)
	go func() { r, _ := runShell("run1", "echo up; sleep 30", "", time.Minute); done <- r }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		shells.Lock()
		_, on := shells.stop["run1"]
		shells.Unlock()
		if on {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // the shell has started
	n.ShellStop("run1")
	select {
	case r := <-done:
		if !r.Stopped || r.TimedOut {
			t.Fatalf("%+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command was not ended")
	}
}

func TestShellKeepsTheFirstOutput(t *testing.T) {
	r, err := runShell("", "head -c 400000 /dev/zero | tr '\\0' x", "", time.Minute)
	if err != nil || !r.Cut || len(r.Output) != shellOutput {
		t.Fatalf("%d bytes, cut %v, %v", len(r.Output), r.Cut, err)
	}
}

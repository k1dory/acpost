//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestShellRunsInTheDirectory(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	r, err := runShell("", "pwd; echo err >&2; exit 3", dir, time.Minute)
	if err != nil || r.Code != 3 || !strings.Contains(r.Output, dir) || !strings.Contains(r.Output, "err") {
		t.Fatalf("%+v %v", r, err)
	}
	// The chat's directory gone: nothing is run (not in the home directory instead).
	mark := filepath.Join(dir, "ran")
	if r, err := runShell("", "touch "+mark, filepath.Join(dir, "none"), time.Minute); err == nil {
		t.Fatalf("a directory that is not there: %+v", r)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the command ran")
	}
	if r, err := runShell("", "pwd", "", time.Minute); err != nil || strings.TrimSpace(r.Output) == "" {
		t.Fatalf("no directory: %+v %v", r, err)
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

// What a command left running behind it ends when the command does.
func TestShellLeavesNothingRunning(t *testing.T) {
	r, err := runShell("", "sleep 600 >/dev/null 2>&1 & echo $!", "", time.Minute)
	if err != nil || r.Code != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(r.Output))
	if err != nil {
		t.Fatalf("%q", r.Output)
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
	}
	t.Fatalf("the background child %d is still running", pid)
}

// Quitting ends what runs, and waits for it; nothing is run after.
func TestShellsEndWithTheApp(t *testing.T) {
	t.Cleanup(func() { shells.Lock(); shells.closed = false; shells.Unlock() })
	done := make(chan ShellResult, 1)
	go func() { r, _ := runShell("quit1", "sleep 600", "", time.Minute); done <- r }()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		shells.Lock()
		_, on := shells.stop["quit1"]
		shells.Unlock()
		if on {
			break
		}
	}
	start := time.Now()
	stopShells()
	// stopShells waited for the run itself (its process ended, its entry gone); its answer reaches
	// the caller a moment after.
	shells.Lock()
	_, still := shells.stop["quit1"]
	shells.Unlock()
	if still || time.Since(start) > 10*time.Second {
		t.Fatalf("stopShells returned with the run still on (%v) after %v", still, time.Since(start))
	}
	select {
	case r := <-done:
		if r.Code != -1 {
			t.Fatalf("%+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command's answer never came")
	}
	if _, err := runShell("", "echo late", "", time.Minute); err == nil {
		t.Fatal("a command ran while the app quits")
	}
}

// Stop that comes before its run is known (the two calls travel apart) still stops it: nothing starts.
func TestShellStopBeforeTheRun(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	(&Node{}).ShellStop("early1")
	r, err := runShell("early1", "touch "+mark, dir, time.Minute)
	if err != nil || !r.Stopped || r.Code != -1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the command ran after it was stopped")
	}
	// Once: the next run of that name is a run.
	if r, err := runShell("early1", "echo ok", dir, time.Minute); err != nil || r.Stopped || !strings.Contains(r.Output, "ok") {
		t.Fatalf("%+v %v", r, err)
	}
}

// The app on a node of another machine runs nothing: the chat's directory is that machine's.
func TestShellRefusedOnAnotherMachinesNode(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	t.Setenv("ACPOST_NODE", "https://node.example:7443")
	if r, err := (&Node{}).Shell("", "touch "+mark, dir); err == nil {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the command ran")
	}
	if r, err := (&Node{}).Shell("", "touch "+mark, ""); err == nil { // no directory named: not the home directory
		t.Fatalf("%+v", r)
	}
	t.Setenv("ACPOST_NODE", "unix://"+filepath.Join(dir, "acpost.sock")) // another socket of this Mac
	if r, err := (&Node{}).Shell("", "echo ok", dir); err != nil || !strings.Contains(r.Output, "ok") {
		t.Fatalf("%+v %v", r, err)
	}
}

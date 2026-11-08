//go:build darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeCodeServer puts script as the pinned VS Code's code-server into data.
func fakeCodeServer(t *testing.T, data, script string) {
	t.Helper()
	bin := filepath.Join(data, "cli", "serve-web", ideCommit, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "code-server"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// alive reports whether pid is still a process (not yet reaped counts: polled until gone).
func gone(pid int, within time.Duration) bool {
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
	}
	return false
}

// Z1: an installer that hangs (Node started by the shell, without exec, holding the output pipe)
// is ended at its deadline with all it started, instead of being waited for.
func TestIDEInstallHungIsBounded(t *testing.T) {
	data := t.TempDir()
	pids := filepath.Join(data, "pids")
	fakeCodeServer(t, data, "sleep 1000 &\necho $! > "+pids+"\nsleep 1000\n")
	old := ideInstallTimeout
	ideInstallTimeout = 1500 * time.Millisecond
	defer func() { ideInstallTimeout = old }()

	res := make(chan error, 1)
	start := time.Now()
	go func() { res <- ideInstall(context.Background(), data) }()
	select {
	case err := <-res:
		if err == nil || !strings.Contains(err.Error(), "in time") {
			t.Fatalf("a hung installer: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ideInstall still waits for a hung installer after 10 s")
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("returned after %v", d)
	}
	b, _ := os.ReadFile(pids)
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("no pid of the installer's child: %q", b)
	}
	if !gone(pid, 3*time.Second) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("the installer's child is still running")
	}
	if _, err := os.Stat(filepath.Join(ideExtDir(data, ideExtension().version), ".acpost-hash")); err == nil {
		t.Error("marked installed after a hung install")
	}
}

// Z1: the app quitting (its context) ends an install under way.
func TestIDEInstallEndsWithContext(t *testing.T) {
	data := t.TempDir()
	fakeCodeServer(t, data, "sleep 1000 &\nsleep 1000\n")
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- ideInstall(ctx, data) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-res:
		if err == nil {
			t.Fatal("no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("install not ended by its context")
	}
}

// The installer's output kept is bounded.
func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{max: 8}
	b.Write([]byte("0123456789"))
	b.Write([]byte("ab"))
	if got := string(b.Bytes()); got != "456789ab" {
		t.Fatalf("%q", got)
	}
}

//go:build darwin

package main

import (
	"context"
	"encoding/json"
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

// Z6/I9: an installer that exits 0 without putting the files there is not taken at its word: an
// error, no mark (tried again next time); a run that installs them is marked.
func TestIDEInstallVerified(t *testing.T) {
	data := t.TempDir()
	dir := "$2/extensions/acpost.acpost-ide-" + ideExtension().version
	fakeCodeServer(t, data, "mkdir -p \""+dir+"\"\necho old > \""+dir+"/workbench.css\"\n")
	mark := filepath.Join(ideExtDir(data, ideExtension().version), ".acpost-hash")
	err := ideInstall(context.Background(), data)
	if err == nil || !strings.Contains(err.Error(), "not the app's") {
		t.Fatalf("an install that changed nothing: %v", err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("marked installed")
	}
	fakeCodeServer(t, data, "set -e\nt=$(mktemp -d)\nunzip -q \"$4\" -d \"$t\"\nmkdir -p \""+dir+"\"\ncp -R \"$t/extension/.\" \""+dir+"/\"\nrm -rf \"$t\"\n")
	if err := ideInstall(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(mark); err != nil || string(b) != ideExtension().hash {
		t.Fatalf("mark: %q %v", b, err)
	}
}

// Review 2: VS Code's installer writes the manifest anew (its own indentation, __metadata added):
// still the app's, marked installed.
func TestIDEInstallManifestRewritten(t *testing.T) {
	data := t.TempDir()
	b, _ := ideExtFS.ReadFile("ide/extension/package.json")
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["__metadata"] = map[string]any{"installedTimestamp": 1, "source": "vsix", "size": 123}
	rewritten, _ := json.MarshalIndent(m, "", "\t")
	mf := filepath.Join(data, "manifest.json")
	os.WriteFile(mf, rewritten, 0o600)
	dir := "$2/extensions/acpost.acpost-ide-" + ideExtension().version
	fakeCodeServer(t, data, "set -e\nt=$(mktemp -d)\nunzip -q \"$4\" -d \"$t\"\nmkdir -p \""+dir+"\"\ncp -R \"$t/extension/.\" \""+dir+"/\"\ncp "+mf+" \""+dir+"/package.json\"\nrm -rf \"$t\"\n")
	if err := ideInstall(context.Background(), data); err != nil {
		t.Fatalf("an install with VS Code's manifest: %v", err)
	}
	// Another change to it is not the app's.
	m["version"] = "9.9.9"
	rewritten, _ = json.MarshalIndent(m, "", "\t")
	os.WriteFile(mf, rewritten, 0o600)
	os.Remove(filepath.Join(ideExtDir(data, ideExtension().version), ".acpost-hash"))
	if err := ideInstall(context.Background(), data); err == nil {
		t.Fatal("a different manifest taken for the app's")
	}
}

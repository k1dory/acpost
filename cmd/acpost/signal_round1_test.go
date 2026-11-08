package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Ctrl-C ends a wait through its context: exit 130, said, promptly; not a kill mid-way.
func TestInterruptEndsAWait(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	id := strings.TrimSpace(e.ok("", "send", "--to", "agent:bot", "--body", "x"))
	cmd := exec.Command(filepath.Join(binDir, "acpost"), "wait", id)
	cmd.Env = append(cleanEnv(), e.vars...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // waiting by now
	cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("still waiting after SIGINT")
	}
	if code := cmd.ProcessState.ExitCode(); code != exitInterrupted || !strings.Contains(errb.String(), "interrupted") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

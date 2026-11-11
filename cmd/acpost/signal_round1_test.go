package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
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
	interrupt(cmd.Process)
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

// startInterrupted starts acpost with stdin left open (nothing typed yet), sends SIGINT once
// ready says so, and returns the exit code and stderr.
func startInterrupted(t *testing.T, e *env, stdin *os.File, ready func() bool, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "acpost"), args...)
	cmd.Env = append(cleanEnv(), e.vars...)
	var errb bytes.Buffer
	cmd.Stdin, cmd.Stderr = stdin, &errb
	if stdin == nil {
		r, w, _ := os.Pipe()
		defer w.Close() // open until the end: the read never ends by itself
		defer r.Close()
		cmd.Stdin = r
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if !until(10*time.Second, ready) {
		cmd.Process.Kill()
		t.Fatal("never ready")
	}
	interrupt(cmd.Process)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatalf("acpost %v: still running after SIGINT", args)
	}
	return cmd.ProcessState.ExitCode(), errb.String()
}

// Ctrl-C while a body is still being read from stdin ends the command: exit 130, not a wait for
// an EOF that does not come.
func TestInterruptWhileReadingStdin(t *testing.T) {
	e := newEnv(t, inbox)
	for _, args := range [][]string{{"send", "--to", "bot", "--body-file", "-"}, {"send", "--to", "bot"}, {"forge", "login", "git.example"}} {
		start := time.Now()
		code, stderr := startInterrupted(t, e, nil, func() bool { return time.Since(start) > 300*time.Millisecond }, args...)
		if code != exitInterrupted {
			t.Fatalf("%v: exit %d %s", args, code, stderr)
		}
	}
}

// Ctrl-C while a token is typed (echo off) ends the login and puts the terminal back as it was.
func TestInterruptedTokenRestoresTheTerminal(t *testing.T) {
	e := newEnv(t, inbox)
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	defer ptmx.Close()
	defer tty.Close()
	before, err := term.GetState(int(tty.Fd()))
	if err != nil {
		t.Skip(err)
	}
	reading := func() bool { // echo off: the token is being read
		now, err := term.GetState(int(tty.Fd()))
		return err == nil && !reflect.DeepEqual(now, before)
	}
	code, stderr := startInterrupted(t, e, tty, reading, "forge", "login", "git.example")
	after, _ := term.GetState(int(tty.Fd()))
	if code != exitInterrupted || !reflect.DeepEqual(after, before) {
		t.Fatalf("exit %d (%s); terminal restored: %v", code, stderr, reflect.DeepEqual(after, before))
	}
}

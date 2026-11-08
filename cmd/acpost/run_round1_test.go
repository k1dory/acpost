package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// childBound is how long a command of a test may run: one that hangs fails its test, named,
// instead of holding the whole run until go test's own timeout.
const childBound = 2 * time.Minute

// runFor is run bounded by d: past it the command is killed and the test fails.
func (e *env) runFor(d time.Duration, stdin string, args ...string) result {
	e.t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "acpost"), args...)
	cmd.Env = append(cleanEnv(), e.vars...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(d):
		cmd.Process.Signal(syscall.SIGKILL)
		<-done
		e.t.Fatalf("acpost %v: still running after %s (killed)\nstderr: %s", args, d, errb.String())
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatal(err)
	}
	return result{out.String(), errb.String(), code}
}

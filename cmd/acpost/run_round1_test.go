package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/k1dory/acpost/internal/store"
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
	if strings.Contains(errb.String(), raceMark) {
		e.t.Errorf("acpost %v: the race detector found a race:\n%s", args, errb.String())
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatal(err)
	}
	return result{out.String(), errb.String(), code}
}

// buildArgs are go build's arguments for a binary the tests run.
func buildArgs(out, pkg string) []string {
	args := []string{"build", "-o", out}
	if raceBuild {
		args = append(args, "-race")
	}
	return append(args, pkg)
}

// textEvents counts the text events a node has of a message (0 when it has none of it yet).
func (e *env) textEvents(id string) int {
	e.t.Helper()
	r := e.run("", "show", id, "--events", "--json")
	var m struct {
		Events []store.Event `json:"events"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.out), &m) != nil {
		return 0
	}
	n := 0
	for _, ev := range m.Events {
		if ev.Kind == "text" {
			n++
		}
	}
	return n
}

// until polls cond every 50ms for up to d; false when it never held.
func until(d time.Duration, cond func() bool) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// raceMark starts a race detector's report.
const raceMark = "WARNING: DATA RACE"

// checkChildren fails the test for what its children said only in their logs: a node started in
// the background by a command (its pid file still there) is stopped first, then every log of the
// environment is read for a race report.
func (e *env) checkChildren() {
	e.t.Helper()
	e.stopAutostarted()
	for _, p := range childProblems(e.dir) {
		e.t.Error(p)
	}
}

// stopAutostarted stops the node a command started in the background, if one still runs: SIGTERM,
// then a wait for it to go (its race report, if any, is written as it exits). Only a process whose
// command line names this environment's configuration: a pid file left by a node gone is no
// reason to signal whatever has its number now.
func (e *env) stopAutostarted() {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(e.sock), "acpost.pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.Contains(string(out), e.config) {
		return
	}
	syscall.Kill(pid, syscall.SIGTERM)
	until(10*time.Second, func() bool { return syscall.Kill(pid, 0) != nil })
}

// childProblems are the race reports in the logs under dir.
func childProblems(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".log") {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), raceMark) {
			out = append(out, "the race detector found a race in a child: "+p+"\n"+string(b))
		}
		return nil
	})
	return out
}

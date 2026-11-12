//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A command of the person's own, run from the chat's composer ("!git status"): in the person's
// login shell, in the chat's directory, its output shown in the chat. It is the person's command,
// run as they would in a terminal; no agent starts one this way.

const (
	shellTimeout = 2 * time.Minute // a command that has not ended by then is ended
	shellOutput  = 256 << 10       // bytes of output kept (the first)
)

// ShellResult is what a command did.
type ShellResult struct {
	Output   string `json:"output"`
	Code     int    `json:"code"`
	Ms       int64  `json:"ms"`
	Cut      bool   `json:"cut,omitempty"` // more output than is kept
	TimedOut bool   `json:"timed_out,omitempty"`
	Stopped  bool   `json:"stopped,omitempty"` // by the person
}

var shells = struct {
	sync.Mutex
	stop map[string]context.CancelFunc
}{stop: map[string]context.CancelFunc{}}

// capped keeps the first max bytes written to it.
type capped struct {
	sync.Mutex
	b   bytes.Buffer
	max int
	cut bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.Lock()
	defer c.Unlock()
	if room := c.max - c.b.Len(); room < len(p) {
		c.cut = true
		if room > 0 {
			c.b.Write(p[:room])
		}
	} else {
		c.b.Write(p)
	}
	return len(p), nil // all taken: the command is not stopped by a full pipe
}

// Shell runs command in cwd (the home directory when there is none) and gives what it printed
// and how it ended. id names the run for ShellStop.
func (n *Node) Shell(id, command, cwd string) (ShellResult, error) {
	return runShell(id, command, cwd, shellTimeout)
}

func runShell(id, command, cwd string, timeout time.Duration) (ShellResult, error) {
	if strings.TrimSpace(command) == "" {
		return ShellResult{}, errors.New("a command to run")
	}
	if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
		cwd, _ = os.UserHomeDir()
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	stopped := false
	if id != "" {
		shells.Lock()
		shells.stop[id] = func() { stopped = true; cancel() }
		shells.Unlock()
		defer func() { shells.Lock(); delete(shells.stop, id); shells.Unlock() }()
	}
	out := &capped{max: shellOutput}
	cmd := exec.CommandContext(ctx, sh, "-lc", command)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")
	cmd.Stdout, cmd.Stderr = out, out
	// All it started ends with it: its own process group, ended whole.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second // what still holds its pipes does not hold the answer
	start := time.Now()
	err := cmd.Run()
	shells.Lock()
	wasStopped := stopped
	shells.Unlock()
	res := ShellResult{Output: strings.ToValidUTF8(out.b.String(), "�"), Ms: time.Since(start).Milliseconds(), Cut: out.cut,
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Stopped: wasStopped}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.Code = exit.ExitCode()
	case res.TimedOut || res.Stopped || errors.Is(err, exec.ErrWaitDelay):
		res.Code = -1
	default:
		return res, err // the shell did not start
	}
	return res, nil
}

// ShellStop ends a running command (and what it started).
func (n *Node) ShellStop(id string) {
	shells.Lock()
	stop := shells.stop[id]
	if stop != nil {
		stop()
	}
	shells.Unlock()
}

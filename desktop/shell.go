//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A command of the person's own, run from the chat's composer ("!git status"): in the person's
// login shell, in the chat's directory, its output shown in the chat. It is the person's command,
// run as they would in a terminal; no agent starts one this way. What it starts ends with it (its
// process group); a process the command itself puts in a session or group of its own (setsid,
// nohup with its own group) is detached by the person's choice, as from a terminal, and is not
// followed.

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
	stop    map[string]context.CancelFunc
	early   map[string]bool // stopped before they were known: not started when they come
	running sync.WaitGroup
	closed  bool // the app is quitting: nothing more is run
}{stop: map[string]context.CancelFunc{}, early: map[string]bool{}}

// stopShells ends every running command and waits for them: none outlives the app.
func stopShells() {
	shells.Lock()
	shells.closed = true
	for _, stop := range shells.stop {
		stop()
	}
	shells.Unlock()
	shells.running.Wait()
}

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
	// On a node of another machine (ACPOST_NODE, not a socket of this Mac) the chats' directories
	// are that machine's: a path of theirs here is another directory, if any. Nothing is run.
	if u := os.Getenv("ACPOST_NODE"); u != "" && !strings.HasPrefix(u, "unix://") {
		return ShellResult{}, errors.New("the app is on another node (ACPOST_NODE): a command here would run on this Mac, not where the chat's files are")
	}
	// The app names the directory (the chat's, a new task's): none named is not "the home
	// directory", it is not known where the command was meant to run.
	if cwd == "" {
		return ShellResult{}, errors.New("no directory to run in: nothing was run")
	}
	return runShell(id, command, cwd, shellTimeout)
}

func runShell(id, command, cwd string, timeout time.Duration) (ShellResult, error) {
	if strings.TrimSpace(command) == "" {
		return ShellResult{}, errors.New("a command to run")
	}
	// No directory: the home directory. One that is named and is not there (the chat's was removed
	// or renamed) runs nothing: the command was meant for that place, not for another.
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	} else if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return ShellResult{}, fmt.Errorf("the chat's directory %s is not there: nothing was run", cwd)
	}
	// The person's login shell when it reads the line below (sh, bash, zsh, dash, ksh); another
	// (fish, nu) has a grammar of its own: zsh then.
	sh := os.Getenv("SHELL")
	switch filepath.Base(sh) {
	case "sh", "bash", "zsh", "dash", "ksh":
	default:
		sh = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	stopped := false
	if id == "" {
		id = fmt.Sprintf("run-%p", &stopped) // every run is known, to be ended with the app
	}
	shells.Lock()
	if shells.closed {
		shells.Unlock()
		return ShellResult{}, errors.New("the app is quitting")
	}
	if shells.early[id] { // Stop came before the run did: it does not start
		delete(shells.early, id)
		shells.Unlock()
		return ShellResult{Code: -1, Stopped: true}, nil
	}
	shells.stop[id] = func() { stopped = true; cancel() }
	shells.running.Add(1)
	shells.Unlock()
	defer func() { shells.Lock(); delete(shells.stop, id); shells.Unlock(); shells.running.Done() }()
	out := &capped{max: shellOutput}
	// The directory is entered again after the shell's own start-up files ran (one of them may
	// change it), and the command is not run if that fails: it runs where it was meant to, or not
	// at all. The command and the directory are handed over as arguments, never spliced into the line.
	cmd := exec.CommandContext(ctx, sh, "-lc", `cd -- "$1" || exit 126; eval "$2"`, "acpost", cwd, command)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")
	cmd.Stdout, cmd.Stderr = out, out
	// All it started ends with it: its own process group, ended whole.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second // what still holds its pipes does not hold the answer
	start := time.Now()
	err := cmd.Run()
	// What it left running behind it ("sleep 600 &") ends with it: nothing of a run outlives its
	// answer, its timeout and its Stop.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
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
	} else if id != "" {
		// Not known yet (its call is still on its way): remembered, so it does not start. A few
		// are kept; a stop of a run already over is forgotten with them.
		if len(shells.early) >= 64 {
			clear(shells.early)
		}
		shells.early[id] = true
	}
	shells.Unlock()
}

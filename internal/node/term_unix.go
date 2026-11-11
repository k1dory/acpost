//go:build unix

package node

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// ptyConsole is the user's login shell on a pseudo-terminal.
type ptyConsole struct {
	*os.File
	cmd *exec.Cmd
}

func startConsole(dir string, env []string, cols, rows int) (console, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir, cmd.Env = dir, env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("start %s: %v", shell, err)
	}
	return &ptyConsole{File: f, cmd: cmd}, nil
}

func (c *ptyConsole) Resize(cols, rows int) error {
	return pty.Setsize(c.File, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (c *ptyConsole) Wait() int {
	var ee *exec.ExitError
	if errors.As(c.cmd.Wait(), &ee) {
		return ee.ExitCode()
	}
	return 0
}

// Kill ends the shell: SIGHUP, then SIGKILL, through os.Process (which signals no process once
// the shell is reaped: on Linux by its pidfd). The rest of the terminal's processes get SIGHUP
// from the kernel as their controlling shell goes, as with any terminal.
func (c *ptyConsole) Kill() {
	if c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Signal(syscall.SIGHUP)
	_ = c.cmd.Process.Kill()
}

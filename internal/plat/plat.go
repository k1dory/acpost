// Package plat is what the node asks of the operating system and Unix and Windows answer
// differently: a lock on a file, a started command with everything it starts as one group, a
// directory no other user reaches, a process left running on its own.
package plat

import (
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
)

// ErrLocked is a lock asked for without waiting that another process holds.
var ErrLocked = errors.New("locked by another process")

// ErrGone is a group asked to stop that has no process left.
var ErrGone = errors.New("no process left in the group")

// StartCancelable starts cmd (one made by exec.CommandContext) as a group its context's end kills
// whole: what cmd started does not outlive a cancelled cmd.
func StartCancelable(cmd *exec.Cmd) (*Group, error) {
	var gp atomic.Pointer[Group]
	cmd.Cancel = func() error {
		if g := gp.Load(); g != nil {
			return g.Kill()
		}
		// The context ended while Start was still at work.
		return killStarting(cmd)
	}
	g, err := Start(cmd)
	if err != nil {
		return nil, err
	}
	gp.Store(g)
	return g, nil
}

// Output runs a cancelable cmd as a group and returns its standard output.
func Output(cmd *exec.Cmd) ([]byte, error) {
	var out capture
	cmd.Stdout = &out
	g, err := StartCancelable(cmd)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	err = cmd.Wait()
	return out.b, err
}

type capture struct{ b []byte }

func (c *capture) Write(p []byte) (int, error) { c.b = append(c.b, p...); return len(p), nil }

// OpenDirError is a directory that should be private and is not.
type OpenDirError struct{ Dir, To, Fix string }

func (e *OpenDirError) Error() string {
	return e.Dir + " is open to other users (" + e.To + "): use a private directory (" + e.Fix + ")"
}

// MkdirPrivate makes dir (and its parents) when it is not there, closed to other users; a
// directory already there is left as it is.
func MkdirPrivate(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return MakePrivate(dir)
}

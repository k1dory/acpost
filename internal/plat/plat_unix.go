//go:build unix

package plat

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Lock takes the exclusive lock on f, held until Unlock or f's close. wait false: ErrLocked when
// another process holds it.
func Lock(f *os.File, wait bool) error {
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(f.Fd()), how)
		switch {
		case err == syscall.EINTR:
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return ErrLocked
		}
		return err
	}
}

// Unlock gives the lock back.
func Unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// Group is a started command and whatever it started: a process group.
type Group struct{ pid int }

// start starts cmd as the leader of a group of its own, and tells publish of the group. lasting
// changes nothing here: a process group does not end with the process that started it.
func start(cmd *exec.Cmd, lasting bool, publish func(*Group)) (*Group, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	g := &Group{pid: cmd.Process.Pid}
	if publish != nil {
		publish(g)
	}
	return g, nil
}

// killStarting kills a command whose Start has not returned its group yet: the group is there
// from the fork.
func killStarting(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// Stop asks the group to end (SIGTERM); ErrGone when nothing of it is left.
func (g *Group) Stop() error {
	if err := syscall.Kill(-g.pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return ErrGone
		}
		return err
	}
	return nil
}

// Kill ends the group at once (SIGKILL: also a stopped or stuck one).
// A group whose processes have all ended but are not reaped yet is refused the signal on macOS
// (EPERM, not ESRCH): there is nothing left to kill, which is what was asked.
func (g *Group) Kill() error {
	if err := syscall.Kill(-g.pid, syscall.SIGKILL); err != nil && err != syscall.EPERM {
		return err
	}
	return nil
}

// Gone says whether nothing of the group is left.
func (g *Group) Gone() bool { return syscall.Kill(-g.pid, 0) == syscall.ESRCH }

// Close lets go of what the group holds of the system (nothing here); the group goes on.
func (g *Group) Close() {}

// Detach makes cmd, once started, a process of its own session: it outlives this one and its
// terminal.
func Detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// Alive says whether a process with this id runs.
func Alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// OpenRead opens a file for reading without waiting on what is not a file (a FIFO with no
// writer, a device): the caller checks what it got.
func OpenRead(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// PrivateDir says whether dir is closed to other users; the error says how to close it.
func PrivateDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return &OpenDirError{Dir: dir, To: fmt.Sprintf("mode %o", st.Mode().Perm()), Fix: "chmod 700"}
	}
	return nil
}

// MkdirPrivate makes dir (and its parents) when it is not there, closed to other users; a
// directory already there is left as it is.
func MkdirPrivate(dir string) error { return os.MkdirAll(dir, 0o700) }

// PrivateFile closes a file to other users.
func PrivateFile(path string) error { return os.Chmod(path, 0o600) }

// Denied says whether err is the system refusing this process a file or a socket.
func Denied(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

// createPrivate makes a new file for this user alone.
func createPrivate(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// ReadFile reads a file that another process may be replacing by a rename meanwhile.
func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// Rename puts a file written whole in place of another, at once.
func Rename(from, to string) error { return os.Rename(from, to) }

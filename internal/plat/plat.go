// Package plat is what the node asks of the operating system and Unix and Windows answer
// differently: a lock on a file, a started command with everything it starts as one group, a
// directory no other user reaches, a process left running on its own.
package plat

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

// ErrLocked is a lock asked for without waiting that another process holds.
var ErrLocked = errors.New("locked by another process")

// ErrGone is a group asked to stop that has no process left.
var ErrGone = errors.New("no process left in the group")

// Start starts cmd as the leader of a group of its own, one that lasts as long as its starter
// wants it: where the system can tell (Windows), a starter that dies takes the group with it.
func Start(cmd *exec.Cmd) (*Group, error) { return start(cmd, true, nil) }

// StartCancelable starts cmd (one made by exec.CommandContext) as a group its context's end kills
// whole: what cmd started does not outlive a cancelled cmd. What it left running at its own end
// goes on, as after any command.
func StartCancelable(cmd *exec.Cmd) (*Group, error) {
	var gp atomic.Pointer[Group]
	cmd.Cancel = func() error {
		if g := gp.Load(); g != nil {
			return g.Kill()
		}
		// The context ended before the group was known (only where it is known after the start).
		return killStarting(cmd)
	}
	return start(cmd, false, func(g *Group) { gp.Store(g) })
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

// FileURI is the file:// link to an absolute path of this machine: file:///C:/dir/name for
// C:\dir\name.
func FileURI(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// URIPath is the path of this machine a file:// link's path names: the reverse of FileURI.
func URIPath(p string) string { return uriPath(p, runtime.GOOS == "windows") }

func uriPath(p string, drives bool) string {
	if drives {
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:] // /C:/dir/name
		}
		return strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

// ConfigDir is where this user's configuration and file-kept secrets are when nothing says
// otherwise: ~/.config/acpost, on Windows %AppData%\acpost.
func ConfigDir() string {
	if runtime.GOOS == "windows" {
		if d, err := os.UserConfigDir(); err == nil {
			return filepath.Join(d, "acpost")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "acpost")
}

// StateDir is where a node keeps its socket and store when nothing says otherwise:
// ~/.local/state/acpost, on Windows %LocalAppData%\acpost (this machine's, not roamed).
func StateDir() string {
	if runtime.GOOS == "windows" {
		if d, err := os.UserCacheDir(); err == nil {
			return filepath.Join(d, "acpost")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "acpost")
}

// CreatePrivateTemp makes a new file in dir, closed to other users from the moment it is there
// (not closed after: who opened it meanwhile would keep it open), named prefix and a random
// part. For what is written whole and then renamed into place.
func CreatePrivateTemp(dir, prefix string) (*os.File, error) {
	for range 100 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		f, err := createPrivate(filepath.Join(dir, prefix+hex.EncodeToString(b[:])+".tmp"))
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, errors.New("no free name for a temporary file in " + dir)
}

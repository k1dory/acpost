//go:build darwin

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// The app's VS Code extension (ide/extension, built by `npm run build` in ide/): its look for the
// IDE. Carried in the app and installed into the app's own VS Code when it is new or changed.

//go:embed all:ide/extension
var ideExtFS embed.FS

// ideCommit is the VS Code the IDE runs (its server, fetched once into the app's folder): fixed,
// so the app's CSS, made against it, does not shift under it. A newer one is taken by changing
// this, after checking the IDE by eye.
const ideCommit = "c4453f8ea430795f941c1b2ff6c9321135a74a02"

type ideExtPack struct {
	hash    string // of every file in it
	version string // its package.json's
	vsix    []byte
	err     error
}

var (
	ideExtOnce sync.Once
	ideExtP    ideExtPack
)

// ideExtension packs the extension (once): a .vsix of its files under extension/, with its hash
// and version.
func ideExtension() ideExtPack {
	ideExtOnce.Do(func() { ideExtP = packExtension(ideExtFS, "ide/extension") })
	return ideExtP
}

func packExtension(fsys fs.FS, root string) (p ideExtPack) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	h := sha256.New()
	err := fs.WalkDir(fsys, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		rel := name[len(root)+1:]
		h.Write([]byte(rel + "\x00"))
		h.Write(b)
		if rel == "package.json" {
			var m struct{ Version string }
			if err := json.Unmarshal(b, &m); err != nil {
				return err
			}
			p.version = m.Version
		}
		w, err := z.Create(path.Join("extension", rel))
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err == nil {
		err = z.Close()
	}
	if err == nil && p.version == "" {
		err = errors.New("the IDE extension has no version")
	}
	p.err, p.vsix, p.hash = err, buf.Bytes(), hex.EncodeToString(h.Sum(nil))
	return p
}

// ideExtDir is where the extension is installed in the app's VS Code.
func ideExtDir(data, version string) string {
	return filepath.Join(data, "server", "extensions", "acpost.acpost-ide-"+version)
}

// ideInstallTimeout bounds one run of VS Code's installer.
var ideInstallTimeout = 2 * time.Minute

// ideInstall installs the extension into the app's VS Code unless this very one is there (its
// hash, kept beside it). With VS Code's own installer: the server's list of extensions stays its.
// ctx ends it early (the app quitting, the start's deadline).
func ideInstall(ctx context.Context, data string) error {
	p := ideExtension()
	if p.err != nil {
		return p.err
	}
	dir := ideExtDir(data, p.version)
	mark := filepath.Join(dir, ".acpost-hash")
	if b, err := os.ReadFile(mark); err == nil && string(b) == p.hash {
		return nil
	}
	bin := filepath.Join(data, "cli", "serve-web", ideCommit, "bin", "code-server")
	f, err := os.CreateTemp("", "acpost-ide-*.vsix")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(p.vsix)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	out, err := runGroup(ctx, ideInstallTimeout, bin, "--server-data-dir", filepath.Join(data, "server"),
		"--install-extension", f.Name(), "--force")
	if err != nil {
		return errors.New("installing the IDE's look: " + err.Error() + ": " + string(bytes.TrimSpace(out)))
	}
	// Its exit is not taken for it: the files there are checked against the app's (an install of
	// the same version that kept, or half wrote, the old files is not marked done, and is tried
	// again on the next IDEStart).
	if err := sameFiles(ideExtFS, "ide/extension", dir); err != nil {
		return errors.New("installing the IDE's look: " + err.Error())
	}
	return os.WriteFile(mark, []byte(p.hash), 0o600)
}

// sameFiles checks that every file under root in fsys is in dir, the same: byte for byte, and
// package.json as JSON without the __metadata VS Code's installer adds to it (it writes the
// manifest anew, its own way).
func sameFiles(fsys fs.FS, root, dir string) error {
	return fs.WalkDir(fsys, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		rel := name[len(root)+1:]
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		same := err == nil && bytes.Equal(got, want)
		if err == nil && !same && rel == "package.json" {
			same = sameManifest(got, want)
		}
		if !same {
			return errors.New(rel + " is not the app's after VS Code's installer")
		}
		return nil
	})
}

func sameManifest(installed, ours []byte) bool {
	var a, b map[string]any
	if json.Unmarshal(installed, &a) != nil || json.Unmarshal(ours, &b) != nil {
		return false
	}
	delete(a, "__metadata")
	return reflect.DeepEqual(a, b)
}

// runGroup runs bin in a process group of its own and gives its output (the last 16 KiB). The
// timeout and ctx end the whole group: VS Code's code-server is a shell script that starts Node
// without exec, so ending the shell alone would leave Node running, holding the output pipe, and
// the wait for it would not end.
func runGroup(ctx context.Context, timeout time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second // a pipe still held after that is closed, not waited for
	out := &tailBuffer{max: 16 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	if cmd.Process != nil {
		// What it left behind in its group (it is the group's leader: the group is its own).
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		err = errors.New("did not finish in time")
	}
	return out.Bytes(), err
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.b...)
}

// shortHash is a short hex hash of b.
func shortHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:8])
}

// What the app asks its VS Code to show: a request is a file in the app's own folder (commands,
// beside the server's), read by the extension in the window that has the folder open. Nothing
// listens anywhere: the folder is the user's alone.

const (
	ideRevealText = 1 << 20         // an edit's text sent at most (more: the file alone, no diff)
	ideRevealKeep = 2 * time.Minute // requests older than this are removed
	ideRevealMax  = 200             // requests kept at most (the extension takes each as it comes)
)

// ideRequest is a request to show a file: as it is, or with an edit as a diff (the text before
// the edit and after it, as the agent reported them; Created: the edit made the file).
type ideRequest struct {
	Folder  string `json:"folder"`
	Path    string `json:"path"`
	At      int64  `json:"at"` // Unix milliseconds: an old request is not shown
	Old     string `json:"old,omitempty"`
	New     string `json:"new,omitempty"`
	Created bool   `json:"created,omitempty"`
	Title   string `json:"title,omitempty"`
}

// IDEReveal shows file in the IDE that has folder open: its edit (old to new) as a diff when given,
// else the file. file is absolute, or from folder.
func (n *Node) IDEReveal(folder, file, old, new string, created bool, title string) error {
	data, err := ideDataDir()
	if err != nil {
		return err
	}
	return ideReveal(filepath.Join(data, "commands"), ideRequest{Folder: folder, Path: file, Old: old, New: new, Created: created, Title: title}, time.Now())
}

func ideReveal(dir string, r ideRequest, now time.Time) error {
	if !filepath.IsAbs(r.Folder) || r.Path == "" || len(r.Path) > 4096 || len(r.Title) > 200 {
		return errors.New("a folder and a file to show")
	}
	if len(r.Old)+len(r.New) > ideRevealText {
		r.Old, r.New, r.Created = "", "", false
	}
	r.At = now.UnixMilli()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Old requests go, and when too many are left (nothing reads them: the IDE is closed) the rest.
	if entries, err := os.ReadDir(dir); err == nil {
		left := 0
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || now.Sub(info.ModTime()) > ideRevealKeep || left >= ideRevealMax {
				os.Remove(filepath.Join(dir, e.Name()))
				continue
			}
			left++
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	// Written apart and renamed in: the extension reads a whole request or none.
	f, err := os.CreateTemp(dir, ".req-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	name := strconv.FormatInt(r.At, 10) + "-" + hex.EncodeToString(rnd[:]) + ".json"
	if werr == nil {
		werr = os.Rename(f.Name(), filepath.Join(dir, name))
	}
	if werr != nil {
		os.Remove(f.Name())
	}
	return werr
}

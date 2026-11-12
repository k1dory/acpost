//go:build darwin

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// Artifacts: what agents hand back — files attached to a reply, or files on this Mac a reply
// links to — to look at in the app, open in their own app, or save.

// Artifact is a file to show: its name, type, size and content (base64).
type Artifact struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int    `json:"size"`
	Path string `json:"path,omitempty"` // a file of this Mac
	Data string `json:"data"`
}

const artifactMax = 20 << 20

// oNoFollowAny is macOS's O_NOFOLLOW_ANY (sys/fcntl.h): no symlink in any part of the path.
const oNoFollowAny = 0x20000000

var errArtifact = errors.New("not a file the app shows: a file of this Mac, outside hidden folders, up to 20 MiB")

// MessageFile is a file of a message: the reply's when the reply and the message both have one
// of that name (an agent sends back a file it was given, changed).
func (n *Node) MessageFile(id, name string) (Artifact, error) {
	files, err := call(n, func(ctx context.Context, c *client.Client) ([]store.File, error) { return c.Files(ctx, id) })
	if err != nil {
		return Artifact{}, err
	}
	var found *store.File
	for i, f := range files {
		// A link has no content here; an empty file is a file.
		if f.Name != name || f.Link() || len(f.Data) == 0 && f.Size > 0 {
			continue
		}
		if found == nil || f.Reply && !found.Reply {
			found = &files[i]
		}
	}
	if found == nil {
		return Artifact{}, errors.New("no such file in the message")
	}
	return Artifact{Name: found.Name, Type: typeOf(found.Name, found.Type, found.Data), Size: len(found.Data), Data: base64.StdEncoding.EncodeToString(found.Data)}, nil
}

// LocalFile is a file of this Mac a chat links to: path absolute, or relative to base (the
// chat's directory). Not in a hidden folder (.ssh, .aws, .config…) nor hidden itself, at most
// artifactMax.
func (n *Node) LocalFile(path, base string) (Artifact, error) {
	p, err := artifactPath(path, base)
	if err != nil {
		return Artifact{}, err
	}
	f, st, err := openLocal(p)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()
	if st.Size() > artifactMax {
		return Artifact{}, errArtifact
	}
	b, err := io.ReadAll(io.LimitReader(f, artifactMax+1))
	if err != nil || len(b) > artifactMax {
		return Artifact{}, errArtifact
	}
	return Artifact{Name: filepath.Base(p), Path: p, Type: typeOf(p, "", b), Size: len(b), Data: base64.StdEncoding.EncodeToString(b)}, nil
}

// openLocal opens a file of this Mac as checked: no symlink anywhere on the resolved path (macOS's
// O_NOFOLLOW_ANY: a folder swapped for a link meanwhile fails), a FIFO does not block
// (O_NONBLOCK), the open file itself regular. The caller reads it up to its limit.
func openLocal(p string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(p, syscall.O_RDONLY|oNoFollowAny|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, errArtifact
	}
	f := os.NewFile(uintptr(fd), p)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, nil, errArtifact
	}
	return f, st, nil
}

// OpenLocal opens a file of this Mac a chat links to in its own app.
func (n *Node) OpenLocal(path, base string) error {
	p, err := artifactPath(path, base)
	if err != nil {
		return err
	}
	return exec.Command("/usr/bin/open", "--", p).Run()
}

// SaveArtifact asks where, and writes the artifact there.
func (n *Node) SaveArtifact(name, data string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	dest, err := n.app.Dialog.SaveFile().SetFilename(filepath.Base(name)).PromptForSingleSelection()
	if err != nil || dest == "" {
		return "", err
	}
	return dest, saveFile(dest, b)
}

// saveFile replaces dest whole, keeping its mode when it exists.
func saveFile(dest string, b []byte) error {
	// A link is written through, as os.WriteFile does: the file at the end of its chain is
	// replaced, in its own directory, and every link stays.
	dest, err := writeTarget(dest)
	if err != nil {
		return err
	}
	var keep *os.FileMode
	if st, err := os.Stat(dest); err == nil {
		m := st.Mode().Perm() // 0000 too: a mode, not "none"
		keep = &m
	}
	return writeAtomic(dest, b, 0o644, keep, true)
}

// OpenArtifact opens an attachment in its own app (a copy in a temporary folder).
func (n *Node) OpenArtifact(name, data string) error {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	dir, err := artifactTemp()
	if err != nil {
		return err
	}
	sub, err := os.MkdirTemp(dir, "open-")
	if err != nil {
		return err
	}
	p := filepath.Join(sub, filepath.Base(name))
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	return exec.Command("/usr/bin/open", "--", p).Run()
}

// artifactTemp is where opened attachments are copied: one folder, its copies older than a day
// removed when it is first used (the app that opened one has read it by then).
var (
	tempOnce sync.Once
	tempDir  string
	tempErr  error
)

func artifactTemp() (string, error) {
	tempOnce.Do(func() {
		tempDir = filepath.Join(os.TempDir(), "acpost-artifacts")
		if tempErr = os.MkdirAll(tempDir, 0o700); tempErr != nil {
			return
		}
		entries, _ := os.ReadDir(tempDir)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
				_ = os.RemoveAll(filepath.Join(tempDir, e.Name()))
			}
		}
	})
	return tempDir, tempErr
}

func artifactPath(path, base string) (string, error) {
	path = strings.TrimSpace(strings.TrimPrefix(path, "file://"))
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		if base == "" || !filepath.IsAbs(base) {
			return "", errArtifact
		}
		path = filepath.Join(base, path)
	}
	p := filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return "", errArtifact
		}
	}
	return p, nil
}

// typeOf is a file's media type: by its name, else what it says, else by its first bytes.
func typeOf(name, said string, b []byte) string {
	// Source and text first: the system's table says .ts is video (MPEG transport stream).
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".rs", ".java", ".kt", ".swift", ".rb", ".sh", ".yaml", ".yml", ".toml", ".sql", ".svelte", ".vue", ".c", ".h", ".cpp", ".log", ".txt", ".csv", ".ini", ".conf", ".json":
		return "text/plain; charset=utf-8"
	}
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	if said != "" && said != "application/octet-stream" {
		return said
	}
	return http.DetectContentType(b)
}

// writeAtomic writes path whole or not at all: a temporary file beside it, synced, then renamed
// over it (replace) or linked in only where there is no file yet (else fs.ErrExist). A
// failure or a crash midway leaves what was there. A new file gets perm under the umask, as
// os.WriteFile gives it; keep (when not nil) is an existing file's mode, kept as it was.
func writeAtomic(path string, b []byte, perm os.FileMode, keep *os.FileMode, replace bool) error {
	dir := filepath.Dir(path)
	mode := perm
	if keep != nil {
		mode = *keep
	}
	var f *os.File
	var err error
	for range 100 {
		var r [8]byte
		rand.Read(r[:])
		// A short name of its own (a destination's name may be as long as a name can be), made
		// as private as the file it replaces from the start: its content is never readable by
		// more than the file's was.
		f, err = os.OpenFile(filepath.Join(dir, ".acpost-tmp-"+hex.EncodeToString(r[:])), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // gone once renamed; after a link, the extra name
	_, err = f.Write(b)
	if afterWrite != nil {
		afterWrite(tmp)
	}
	if err == nil && keep != nil {
		err = f.Chmod(*keep)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if replace {
		err = os.Rename(tmp, path)
	} else {
		err = os.Link(tmp, path)
	}
	if err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // the new name itself survives a crash
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// afterWrite, in tests, looks at the temporary file once the content is in it.
var afterWrite func(tmp string)

// writeTarget is the file a write to p reaches, read as the system reads a path: component by
// component, each link met replaced by its target (read from the directory reached so far), a
// ".." going up from the directory actually reached, never from the text of the path. Only the
// last component may be missing (a link to a file not there yet makes it). A cycle, or a chain
// too long, is an error.
func writeTarget(p string) (string, error) {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = wd + string(filepath.Separator) + p
	}
	rest := strings.Split(p, string(filepath.Separator))
	cur, hops := string(filepath.Separator), 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur) // cur has no links in it: its parent is the real one
			continue
		}
		next := filepath.Join(cur, c)
		st, err := os.Lstat(next)
		// A trailing "/" or "/." says "a directory": only a name with nothing at all after it
		// may be a file, missing or not.
		if errors.Is(err, fs.ErrNotExist) && len(rest) == 0 {
			return next, nil
		}
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			if hops++; hops > 40 {
				return "", errors.New(p + ": too many links (a cycle?)")
			}
			to, err := os.Readlink(next)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(to) {
				cur = string(filepath.Separator)
			}
			rest = append(strings.Split(to, string(filepath.Separator)), rest...)
			continue
		}
		if !st.IsDir() && len(rest) > 0 {
			return "", &fs.PathError{Op: "save", Path: next, Err: syscall.ENOTDIR}
		}
		cur = next
	}
	// The walk ended on a directory (a link to ".", "..", a folder): the system would not
	// write it as a file.
	if st, err := os.Lstat(cur); err != nil {
		return "", err
	} else if st.IsDir() {
		return "", &fs.PathError{Op: "save", Path: cur, Err: syscall.EISDIR}
	}
	return cur, nil
}

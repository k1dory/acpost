//go:build darwin

package main

import (
	"archive/zip"
	"bytes"
	"context"
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
	"sync"
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

// ideInstall installs the extension into the app's VS Code unless this very one is there (its
// hash, kept beside it). With VS Code's own installer: the server's list of extensions stays its.
func ideInstall(data string) error {
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--server-data-dir", filepath.Join(data, "server"),
		"--install-extension", f.Name(), "--force").CombinedOutput()
	if err != nil {
		return errors.New("installing the IDE's look: " + err.Error() + ": " + string(bytes.TrimSpace(out)))
	}
	return os.WriteFile(mark, []byte(p.hash), 0o600)
}

// shortHash is a short hex hash of b.
func shortHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:8])
}

//go:build darwin

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/k1dory/acpost/internal/client"
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

var errArtifact = errors.New("not a file the app shows: a file of this Mac, outside hidden folders, up to 20 MiB")

// MessageFile is a file of a message (a reply's attachment).
func (n *Node) MessageFile(id, name string) (Artifact, error) {
	files, err := call(n, func(ctx context.Context, c *client.Client) ([]storeFile, error) { return filesOf(ctx, c, id) })
	if err != nil {
		return Artifact{}, err
	}
	for _, f := range files {
		if f.Name == name && len(f.Data) > 0 {
			return Artifact{Name: f.Name, Type: typeOf(f.Name, f.Type, f.Data), Size: len(f.Data), Data: base64.StdEncoding.EncodeToString(f.Data)}, nil
		}
	}
	return Artifact{}, errors.New("no such file in the message")
}

// LocalFile is a file of this Mac a chat links to: path absolute, or relative to base (the
// chat's directory). Not in a hidden folder (.ssh, .aws, .config…) nor hidden itself, at most
// artifactMax.
func (n *Node) LocalFile(path, base string) (Artifact, error) {
	p, err := artifactPath(path, base)
	if err != nil {
		return Artifact{}, err
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > artifactMax {
		return Artifact{}, errArtifact
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{Name: filepath.Base(p), Path: p, Type: typeOf(p, "", b), Size: len(b), Data: base64.StdEncoding.EncodeToString(b)}, nil
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
	return dest, os.WriteFile(dest, b, 0o644)
}

// OpenArtifact opens an attachment in its own app (a copy in a temporary folder).
func (n *Node) OpenArtifact(name, data string) error {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "acpost-artifact-")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	return exec.Command("/usr/bin/open", "--", p).Run()
}

func artifactPath(path, base string) (string, error) {
	path = strings.TrimSpace(strings.TrimPrefix(path, "file://"))
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	}
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
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".go", ".py", ".ts", ".tsx", ".js", ".rs", ".java", ".kt", ".swift", ".rb", ".sh", ".yaml", ".yml", ".toml", ".sql", ".svelte", ".vue", ".c", ".h", ".cpp", ".log", ".txt", ".csv", ".ini", ".conf":
		return "text/plain"
	}
	if said != "" && said != "application/octet-stream" {
		return said
	}
	return http.DetectContentType(b)
}

// storeFile is a message's file as the node gives it.
type storeFile struct {
	Name  string
	Type  string
	Reply bool
	Data  []byte
}

func filesOf(ctx context.Context, c *client.Client, id string) ([]storeFile, error) {
	fs, err := c.Files(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]storeFile, 0, len(fs))
	for _, f := range fs {
		out = append(out, storeFile{Name: f.Name, Type: f.Type, Reply: f.Reply, Data: f.Data})
	}
	return out, nil
}


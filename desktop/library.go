//go:build darwin

package main

import (
	"context"
	"encoding/base64"
	"io"
	"strings"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// The artifacts library: every chat's artifacts in one place, listed by the node a page at a time;
// each card shows a little of its file, asked for only when the card comes into view.

const (
	previewText  = 8 << 10 // the start of a text a card shows
	previewImage = 2 << 20 // a picture a card shows whole, up to this
)

// Artifacts lists a page of the artifacts of every chat, newest first: match filters by name or
// address, before is the previous page's next.
func (n *Node) Artifacts(match, before string, limit int) (client.Artifacts, error) {
	return call(n, func(ctx context.Context, c *client.Client) (client.Artifacts, error) {
		return c.Artifacts(ctx, match, before, limit)
	})
}

// ArtifactPreview is what a card shows of an artifact: a picture whole when it is small, the start
// of a text; of anything else (a page, a PDF, a bigger picture) its name, type and size, no
// content. msg and name are a reply's file; path is a file of this Mac.
func (n *Node) ArtifactPreview(msg, name, path string) (Artifact, error) {
	if path != "" {
		return localPreview(path)
	}
	head := func(max int) (store.File, error) {
		return call(n, func(ctx context.Context, c *client.Client) (store.File, error) {
			return c.ArtifactHead(ctx, msg, name, max)
		})
	}
	f, err := head(previewText)
	if err != nil {
		return Artifact{}, err
	}
	ty := typeOf(f.Name, f.Type, f.Data)
	if f.Link() {
		return Artifact{Name: f.Name, Type: ty, Size: f.Size}, nil
	}
	data := f.Data
	switch previewKind(ty) {
	case "image":
		if f.Size > previewImage {
			data = nil
		} else if f.Size > len(data) {
			if f, err = head(previewImage); err != nil {
				return Artifact{}, err
			}
			data = f.Data
		}
	case "text":
	default:
		data = nil
	}
	return Artifact{Name: f.Name, Type: ty, Size: f.Size, Data: base64.StdEncoding.EncodeToString(data)}, nil
}

// localPreview is a card's look at a file of this Mac, opened as LocalFile opens one.
func localPreview(path string) (Artifact, error) {
	p, err := artifactPath(path, "")
	if err != nil {
		return Artifact{}, err
	}
	f, st, err := openLocal(p)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()
	// What LocalFile would refuse to show is not shown on a card either.
	if st.Size() > artifactMax {
		return Artifact{}, errArtifact
	}
	b, err := io.ReadAll(io.LimitReader(f, previewText))
	if err != nil {
		return Artifact{}, errArtifact
	}
	a := Artifact{Name: st.Name(), Path: p, Type: typeOf(p, "", b), Size: int(st.Size())}
	switch previewKind(a.Type) {
	case "image":
		if st.Size() > previewImage {
			return a, nil
		}
		rest, err := io.ReadAll(io.LimitReader(f, previewImage+1-int64(len(b))))
		if err != nil || len(b)+len(rest) > previewImage {
			return a, nil
		}
		b = append(b, rest...)
	case "text":
	default:
		return a, nil
	}
	a.Data = base64.StdEncoding.EncodeToString(b)
	return a, nil
}

// previewKind is how a card shows a type: a picture, the start of a text, or "" (an icon only).
// A page is not shown as its source.
func previewKind(ty string) string {
	ty = strings.ToLower(strings.TrimSpace(strings.Split(ty, ";")[0]))
	switch {
	case strings.HasPrefix(ty, "image/"):
		return "image"
	case ty == "text/html":
		return ""
	case strings.HasPrefix(ty, "text/"), ty == "application/json", ty == "application/xml", ty == "application/yaml", ty == "application/toml":
		return "text"
	}
	return ""
}

package node

// Files an ACP agent hands back in its answer: image, resource and resource_link blocks of its
// agent_message_chunk updates. Each becomes a file of the reply, kept the cheapest way that loses
// nothing:
//
//   - a resource_link is kept as the link;
//   - content the agent was given in this turn (its copy, or the embedded file) is the message's
//     file again, not new content;
//   - embedded content whose file:// URI names a file on disk with the same bytes is a link to it;
//   - anything else is content, which the store keeps once per SHA-256.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/k1dory/acpost/internal/plat"
	"github.com/k1dory/acpost/internal/store"
)

// replyBlock is a file-carrying block of an agent's answer, as it came.
type replyBlock struct {
	name, uri, typ string
	size           int    // a link's, as the agent said
	data           []byte // embedded content (nil for a link)
	link           bool
}

// fileBlock takes the file out of a content block; ok is false for text and the like.
func fileBlock(c acp.ContentBlock) (replyBlock, bool) {
	switch {
	case c.ResourceLink != nil:
		l := c.ResourceLink
		b := replyBlock{name: l.Name, uri: l.Uri, link: true}
		if l.MimeType != nil {
			b.typ = *l.MimeType
		}
		if l.Size != nil {
			b.size = *l.Size
		}
		return b, true
	case c.Image != nil:
		data, err := base64.StdEncoding.DecodeString(c.Image.Data)
		if err != nil {
			return replyBlock{}, false
		}
		b := replyBlock{typ: c.Image.MimeType, data: data}
		if c.Image.Uri != nil {
			b.uri = *c.Image.Uri
		}
		return b, true
	case c.Audio != nil:
		data, err := base64.StdEncoding.DecodeString(c.Audio.Data)
		if err != nil {
			return replyBlock{}, false
		}
		return replyBlock{typ: c.Audio.MimeType, data: data}, true
	case c.Resource != nil && c.Resource.Resource.TextResourceContents != nil:
		r := c.Resource.Resource.TextResourceContents
		b := replyBlock{uri: r.Uri, data: []byte(r.Text)}
		if r.MimeType != nil {
			b.typ = *r.MimeType
		}
		return b, true
	case c.Resource != nil && c.Resource.Resource.BlobResourceContents != nil:
		r := c.Resource.Resource.BlobResourceContents
		data, err := base64.StdEncoding.DecodeString(r.Blob)
		if err != nil {
			return replyBlock{}, false
		}
		b := replyBlock{uri: r.Uri, data: data}
		if r.MimeType != nil {
			b.typ = *r.MimeType
		}
		return b, true
	}
	return replyBlock{}, false
}

// replyFilesOf makes the reply's files of what the agent handed back; taken are the names already
// on the reply (acpost attach). What cannot be kept is said in notes.
//
// For a sender on another node (remote) a file:// link would not open there, and the node does not
// read files on the agent's behalf (its sandbox may not reach them): such a link is not kept, and
// said; embedded content stays content.
func (n *Node) replyFilesOf(blocks []replyBlock, tf *turnFiles, taken []store.File, remote bool) (files []store.File, notes []string) {
	names := map[string]bool{}
	for _, f := range taken {
		names[f.Name] = true
	}
	for i, b := range blocks {
		f := store.File{Type: b.typ, Size: b.size}
		if validFileName(b.name) == nil {
			f.Name = b.name // the name the agent gave wins over any the URI suggests
		}
		known, ok := tf.lookup(b.uri)
		// The file it was given comes back as that file only unchanged: a link to it, or the same
		// bytes. Edited content is new content.
		same := ok && (b.link || (!known.Link() && bytes.Equal(b.data, known.Data)))
		switch {
		case same:
			f.Data, f.Type, f.Size = known.Data, known.Type, known.Size
			if known.Link() {
				f.Data, f.URI = nil, known.URI
			}
			if f.Name == "" {
				f.Name = known.Name
			}
		case ok:
			f.Data = b.data
			if f.Name == "" {
				f.Name = known.Name
			}
		case b.link && remote && isFileLink(b.uri):
			notes = append(notes, fmt.Sprintf("%s not kept: a file:// link, and the sender is on another node; the agent attaches a copy with `acpost attach FILE`", cmp.Or(f.Name, b.uri)))
			continue
		case b.link:
			f.URI = b.uri
		case !remote && onDisk(b.uri, b.data):
			// The agent embedded a file it has on disk: that file is where it is kept.
			f.URI, f.Size = b.uri, len(b.data)
		default:
			f.Data = b.data
		}
		if f.Name == "" {
			f.Name = replyName(b, i)
		}
		f.Name = uniqueName(names, f.Name)
		if f.Link() {
			if err := validLink(&f); err != nil {
				notes = append(notes, fmt.Sprintf("%s not kept: %v", f.Name, err))
				continue
			}
		}
		files = append(files, f)
	}
	return files, notes
}

// lookup finds a URI or path the agent was given in this turn.
func (tf *turnFiles) lookup(uri string) (store.File, bool) {
	if tf == nil || uri == "" {
		return store.File{}, false
	}
	if f, ok := tf.byURI[uri]; ok {
		return f, true
	}
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" {
		f, ok := tf.byURI[plat.URIPath(u.Path)]
		return f, ok
	}
	return store.File{}, false
}

// onDisk says whether uri is a file:// path whose file holds exactly data now.
func onDisk(uri string, data []byte) bool {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || !filepath.IsAbs(plat.URIPath(u.Path)) {
		return false
	}
	// One open, non-blocking, checked on its descriptor, read no further than the content: a path
	// swapped for a FIFO or a device is not waited on or read without end.
	fd, err := plat.OpenRead(plat.URIPath(u.Path))
	if err != nil {
		return false
	}
	defer fd.Close()
	st, err := fd.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != int64(len(data)) {
		return false
	}
	got, err := io.ReadAll(io.LimitReader(fd, int64(len(data))+1))
	return err == nil && bytes.Equal(got, data)
}

// replyName names a block without a name: the last part of its URI, or its kind and place.
func replyName(b replyBlock, i int) string {
	if u, err := url.Parse(b.uri); err == nil && u.Path != "" {
		if base := path.Base(u.Path); validFileName(base) == nil {
			return base
		}
	}
	kind := "file"
	mt := mediaType(b.typ)
	switch {
	case strings.HasPrefix(mt, "image/"):
		kind = "image"
	case strings.HasPrefix(mt, "audio/"):
		kind = "audio"
	}
	ext := ""
	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		ext = exts[0]
	}
	return kind + "-" + strconv.Itoa(i+1) + ext
}

// uniqueName is name, or name-2, name-3… when the reply has it already.
func uniqueName(taken map[string]bool, name string) string {
	out := name
	ext := path.Ext(name)
	for k := 2; taken[out]; k++ {
		out = strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(k) + ext
	}
	taken[out] = true
	return out
}

// keepReplyFiles stores what the agent handed back with its reply, within the reply's limits; what
// does not fit is said in an event, and the reply's text stays. It returns what it said.
func (n *Node) keepReplyFiles(ctx context.Context, m store.Message, blocks []replyBlock, dropped int, tf *turnFiles) (said []string) {
	if dropped > 0 {
		said = append(said, fmt.Sprintf("the agent handed back %d more files than the reply keeps (limits.max_files, max_files_size)", dropped))
		n.notice(ctx, m.ID, said[0])
	}
	if len(blocks) == 0 {
		return said
	}
	cur, err := n.st.Get(ctx, m.ID)
	if err != nil {
		return said
	}
	have := replyFiles(cur.Files)
	files, notes := n.replyFilesOf(blocks, tf, have, n.senderElsewhere(cur))
	// Kept in order while they fit with what the reply has already (acpost attach); the rest is said.
	count, size := len(have), int64(0)
	for _, f := range have {
		if !f.Link() {
			size += int64(f.Size)
		}
	}
	var kept []store.File
	for _, f := range files {
		add := int64(0)
		if !f.Link() {
			add = int64(len(f.Data))
		}
		if count+1 > n.cfg.Limits.MaxFiles || size+add > int64(n.cfg.Limits.MaxFilesSize) {
			notes = append(notes, fmt.Sprintf("%s not kept: the reply is at limits.max_files (%d) or max_files_size (%d)", f.Name, n.cfg.Limits.MaxFiles, n.cfg.Limits.MaxFilesSize))
			continue
		}
		one := []store.File{f}
		if err := n.checkFiles(one, kept, "the reply"); err != nil {
			notes = append(notes, fmt.Sprintf("%s not kept: %v", f.Name, err))
			continue
		}
		f = one[0] // with its type filled in
		count, size = count+1, size+add
		kept = append(kept, f)
	}
	files = kept
	if len(files) > 0 {
		lim := store.FileLimits{Count: n.cfg.Limits.MaxFiles, Size: int64(n.cfg.Limits.MaxFilesSize)}
		if err := n.st.AttachReply(ctx, m.ID, m.LeaseID, files, lim); err != nil {
			notes = append(notes, "the files the agent handed back were not kept: "+n.fileErr(err, m.ID).Error())
		}
	}
	for _, note := range notes {
		n.notice(ctx, m.ID, note)
	}
	return append(said, notes...)
}

// notice records a notice event on a message.
func (n *Node) notice(ctx context.Context, id, text string) {
	b, _ := json.Marshal(text)
	_ = n.addEvent(ctx, id, "notice", string(b), false)
}

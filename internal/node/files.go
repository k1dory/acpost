package node

// Files attached to messages and replies: checked against the limits, typed, and handed to whoever
// may see the message.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/k1dory/acpost/internal/plat"
	"github.com/k1dory/acpost/internal/store"
)

// checkFiles validates files to attach next to have (those already attached to the same side),
// filling in their types. side names the side for errors ("the message", "the reply").
func (n *Node) checkFiles(files, have []store.File, side string) error {
	lim := n.cfg.Limits
	if len(files) == 0 {
		return nil
	}
	if len(files)+len(have) > lim.MaxFiles {
		return errf("refused", "%s would have %d files, over limits.max_files (%d)", side, len(files)+len(have), lim.MaxFiles)
	}
	size := int64(0)
	earlier, now := map[string]bool{}, map[string]bool{}
	for _, f := range have {
		if !f.Link() {
			size += int64(f.Size)
		}
		earlier[f.Name] = true
	}
	for i := range files {
		f := &files[i]
		if err := validFileName(f.Name); err != nil {
			return err
		}
		f.SHA256 = "" // the store's to say
		if f.Link() {
			if err := validLink(f); err != nil {
				return err
			}
		}
		switch {
		case now[f.Name]:
			return errh("conflict", "files go by their base name: rename one, or attach an archive", "two of the files are named %s", f.Name)
		case earlier[f.Name]:
			return errh("conflict", "rename the file; what is attached stays", "%s already has a file %s (attached earlier)", side, f.Name)
		}
		now[f.Name] = true
		f.Reply = false
		storedForm(f) // node.go: what asStored compares a repeated reply by
		if !f.Link() {
			size += int64(len(f.Data))
		}
	}
	if size > int64(lim.MaxFilesSize) {
		return errf("refused", "the files of %s are %d bytes together, over limits.max_files_size (%d)", side, size, lim.MaxFilesSize)
	}
	return nil
}

// maxLinkSize bounds the size a link may claim (it is the sender's word, shown, never read).
const maxLinkSize = 1 << 50

// validLink checks a link: an absolute file:// path, or an http(s) URL; no content with it.
func validLink(f *store.File) error {
	bad := func(why string) error {
		return errh("invalid", "a link is file:///absolute/path or an http(s) URL, without data", "link %s: %s", f.Name, why)
	}
	if len(f.Data) > 0 {
		return bad("a file is content or a link, not both")
	}
	if len(f.URI) > 2048 || strings.ContainsFunc(f.URI, unicode.IsControl) {
		return bad("the URI is too long or has control characters")
	}
	u, err := url.Parse(f.URI)
	if err != nil {
		return bad(err.Error())
	}
	switch u.Scheme {
	case "file":
		if _, local := plat.LocalFile(u.Path); u.Host != "" && u.Host != "localhost" || !local {
			return bad("a file link names an absolute path on this machine")
		}
	case "http", "https":
		if u.Host == "" {
			return bad("no host")
		}
	default:
		return bad("scheme " + u.Scheme)
	}
	f.Size = linkSize(f.Size)
	return nil
}

func validFileName(name string) error {
	bad := name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) ||
		strings.ContainsAny(name, `/\`) || strings.ContainsFunc(name, unicode.IsControl)
	if bad {
		return errh("invalid", "a file is named by its base name: letters, digits, spaces, dots, no / or \\", "bad file name %q", name)
	}
	return nil
}

// fileType is the media type of a file: the one given if it parses, else the extension's, else
// what its first bytes look like.
func fileType(name, given string, data []byte) string {
	if t := knownType(name, given); t != "" {
		return t
	}
	return http.DetectContentType(data)
}

// linkType is a link's type: given, or by its name; else nothing says what it is.
func linkType(name, given string) string {
	if t := knownType(name, given); t != "" {
		return t
	}
	return "application/octet-stream"
}

// knownType is a file's type as given or as its extension says ("" for neither).
func knownType(name, given string) string {
	// A given type is taken when it is a short, plain media type (it goes into the agent's prompt).
	if given != "" && len(given) <= 127 {
		if _, _, err := mime.ParseMediaType(given); err == nil {
			return given
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	if t := textTypes[ext]; t != "" {
		return t
	}
	return mime.TypeByExtension(ext)
}

// textTypes are text formats agents trade that the system's table may not know.
var textTypes = map[string]string{
	".md": "text/markdown; charset=utf-8", ".markdown": "text/markdown; charset=utf-8",
	".yaml": "application/yaml", ".yml": "application/yaml", ".toml": "application/toml",
	".diff": "text/x-diff; charset=utf-8", ".patch": "text/x-diff; charset=utf-8", ".log": "text/plain; charset=utf-8",
}

// isText says whether a file reads as text: an agent gets it embedded rather than as a link.
func isText(f store.File) bool {
	t, _, _ := mime.ParseMediaType(f.Type)
	text := strings.HasPrefix(t, "text/") || t == "application/json" || t == "application/xml" ||
		t == "application/yaml" || t == "application/x-yaml" || t == "application/toml" || t == "application/javascript"
	return text && utf8.Valid(f.Data)
}

// Files returns the files of a message p may see, with their data.
func (n *Node) Files(ctx context.Context, p Principal, id string) ([]store.File, error) {
	if _, err := n.Get(ctx, p, id); err != nil {
		return nil, err
	}
	return n.st.FilesOf(ctx, id)
}

// Attach adds files to the reply an agent process is working on: an ACP agent's final text is its
// reply, and these go with it.
//
// turn is what AttachTurn said before the files were read: files uploaded during one attempt go
// to that attempt, or nowhere.
func (n *Node) Attach(ctx context.Context, turn Turn, files []store.File) (string, error) {
	if err := n.checkFiles(files, nil, "the reply"); err != nil {
		return "", err
	}
	if _, ok := localLink(files); ok {
		if m, err := n.st.Get(ctx, turn.id); err != nil || n.senderElsewhere(m) {
			if err := noLocalLinks(files, m.From, attachCopyHint); err != nil {
				return "", err
			}
		}
	}
	lim := store.FileLimits{Count: n.cfg.Limits.MaxFiles, Size: int64(n.cfg.Limits.MaxFilesSize)}
	if err := n.st.AttachReply(ctx, turn.id, turn.lease, files, lim); err != nil {
		return "", n.fileErr(err, turn.id)
	}
	return turn.id, nil
}

// Turn is an attempt of a message an agent process is running.
type Turn = turnRef

// AttachTurn is the attempt p is running, to attach files to; an error if p runs none.
func (n *Node) AttachTurn(p Principal) (Turn, error) {
	turn := n.turnOf(p)
	if p.Kind != "agent" || turn.id == "" {
		hint := "to answer a message you took with recv: `acpost reply ID --lease L --attach FILE`"
		if p.Kind != "agent" {
			hint = "`acpost attach` works inside an agent's turn, with the ACPOST_TOKEN the node gives the agent (here there is none); " + hint
		}
		return Turn{}, errh("invalid", hint, "%s is not in an agent's turn: there is no reply to attach to", p.Address())
	}
	return turn, nil
}

func (n *Node) fileErr(err error, id string) error {
	switch {
	case errors.Is(err, store.ErrFinal), errors.Is(err, store.ErrLeaseLost):
		return errf("conflict", "this turn no longer holds %s (its reply is sent, or another attempt has it)", id)
	case errors.Is(err, store.ErrFileLimits):
		return errf("refused", "%v", err)
	case errors.Is(err, store.ErrFileExists):
		return errh("conflict", "give each file its own name", "%v", err)
	case errors.Is(err, store.ErrStoreFull):
		return errf("refused", "%v", err)
	}
	return err
}

func replyFiles(files []store.File) []store.File { return pick(files, true) }

// checkListed checks the files a listing announces (names, sizes, no data) against this node's
// limits, before they are fetched.
func (n *Node) checkListed(files []store.File, side string) error {
	lim := n.cfg.Limits
	if len(files) > lim.MaxFiles {
		return errf("refused", "%s has %d files, over limits.max_files (%d)", side, len(files), lim.MaxFiles)
	}
	var size int64
	for _, f := range files {
		if err := validFileName(f.Name); err != nil {
			return err
		}
		if !f.Link() {
			size += int64(f.Size)
		}
	}
	if size > int64(lim.MaxFilesSize) {
		return errf("refused", "the files of %s are %d bytes together, over limits.max_files_size (%d)", side, size, lim.MaxFilesSize)
	}
	return nil
}

func pick(files []store.File, reply bool) []store.File {
	var out []store.File
	for _, f := range files {
		if f.Reply == reply {
			out = append(out, f)
		}
	}
	return out
}

// attached is a message's file as an agent's turn gets it: a link as it is, content embedded in
// the prompt or, when it is not, a copy on disk at path.
type attached struct {
	store.File
	path     string
	embedded bool
	block    *acp.ContentBlock // the embedded block, when the agent takes this file embedded
}

// uri is where the agent finds the file: the link, the copy, or (embedded) the message's own name.
func (a *attached) uri(msgID string) string {
	switch {
	case a.Link():
		return a.URI
	case a.embedded:
		return "acpost://" + msgID + "/files/" + url.PathEscape(a.Name)
	}
	return fileURI(a.path)
}

func (a *attached) linkBlock(msgID string) acp.ContentBlock {
	link := acp.ResourceLinkBlock(a.Name, a.uri(msgID))
	typ := a.Type
	link.ResourceLink.MimeType = &typ
	if a.Size > 0 || !a.Link() {
		size := a.Size
		link.ResourceLink.Size = &size
	}
	return link
}

// turnFiles are a turn's attached files and where their copies are: the agent may hand them back,
// and what it hands back is then the stored file, not new content.
type turnFiles struct {
	dir   string
	byURI map[string]store.File // the URIs and paths the agent was given → the message's file
}

// promptBlocks is the prompt of a turn: the text, then each attached file as the agent takes it —
// a link as the link, text embedded, an image inline, anything else (or for an agent that takes
// neither) a link to a copy on disk. Only those copies are written, and they go when the turn
// ends (cleanup).
func (s *acpSlot) promptBlocks(ctx context.Context, m store.Message, caps acp.PromptCapabilities) ([]acp.ContentBlock, *turnFiles, func(), error) {
	nothing := func() {}
	tf := &turnFiles{byURI: map[string]store.File{}}
	if len(m.Files) == 0 && jsonLen([]byte(m.Body)) <= maxPrompt-promptOverhead {
		return []acp.ContentBlock{acp.TextBlock(s.prompt(m, nil))}, tf, nothing, nil
	}
	var all []store.File
	if len(m.Files) > 0 {
		var err error
		if all, err = s.a.n.st.FilesOf(ctx, m.ID); err != nil {
			return nil, nil, nothing, err
		}
	}
	dir, err := os.MkdirTemp(s.a.n.turns, m.ID+"-")
	if err != nil {
		return nil, nil, nothing, err
	}
	tf.dir = dir
	cleanup := func() { _ = os.RemoveAll(dir) }
	fail := func(err error) ([]acp.ContentBlock, *turnFiles, func(), error) {
		cleanup()
		return nil, nil, nothing, err
	}

	files := make([]*attached, 0, len(all))
	for i, f := range pick(all, false) {
		// A directory per file: names that differ only in case are two files on any file system.
		a := &attached{File: f, path: filepath.Join(dir, strconv.Itoa(i), f.Name)}
		if !f.Link() {
			// What the agent takes embedded, within the budget for embedded content.
			var b acp.ContentBlock
			switch {
			case caps.EmbeddedContext && len(f.Data) <= embedBudget && isText(f):
				typ := f.Type
				a.embedded = true
				b = acp.ResourceBlock(acp.EmbeddedResourceResource{
					TextResourceContents: &acp.TextResourceContents{Uri: a.uri(m.ID), Text: string(f.Data), MimeType: &typ}})
				a.block = &b
			case caps.Image && isImage(f.Type) && base64.StdEncoding.EncodedLen(len(f.Data)) <= embedBudget:
				a.embedded = true
				b = acp.ImageBlock(base64.StdEncoding.EncodeToString(f.Data), mediaType(f.Type))
				a.block = &b
			}
		}
		files = append(files, a)
	}

	// The request stays inside the frame an agent reads (the Go SDK's is 10 MiB): its size is
	// measured as the JSON it is. What does not fit goes as a link instead, the biggest first;
	// then the body goes as a file the prompt points to.
	build := func() ([]acp.ContentBlock, int, int) {
		list := make([]attached, len(files))
		for i, a := range files {
			list[i] = *a
		}
		text := s.prompt(m, list)
		blocks := []acp.ContentBlock{acp.TextBlock(text)}
		embedded := 0
		for _, a := range files {
			if a.embedded {
				blocks = append(blocks, *a.block)
				embedded += blockLen(*a.block)
			} else {
				blocks = append(blocks, a.linkBlock(m.ID))
			}
		}
		return blocks, blocksLen(blocks), embedded
	}
	blocks, used, embedded := build()
	movedBody := false
	for used > maxPrompt-promptOverhead || embedded > embedBudget {
		var biggest *attached
		for _, a := range files {
			if a.embedded && (biggest == nil || len(a.Data) > len(biggest.Data)) {
				biggest = a
			}
		}
		switch {
		case biggest != nil:
			biggest.embedded, biggest.block = false, nil
		case !movedBody:
			path := filepath.Join(dir, "message.txt")
			if err := os.WriteFile(path, []byte(m.Body), 0o600); err != nil {
				return fail(err)
			}
			m.Body = fmt.Sprintf("[acpost] The message is %d bytes, too large to send in the prompt: it is in %s (until this turn ends). Read it there; it is the task.", len(m.Body), path)
			movedBody = true
		default:
			return fail(fmt.Errorf("the names and links of %d files alone are %d bytes, over what one prompt carries (%d): send fewer files", len(files), used, maxPrompt))
		}
		blocks, used, embedded = build()
	}

	// Copies only of what goes as a copy.
	for _, a := range files {
		tf.byURI[a.uri(m.ID)] = a.File
		if a.Link() || a.embedded {
			continue
		}
		if err := os.Mkdir(filepath.Dir(a.path), 0o700); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(a.path, a.Data, 0o600); err != nil {
			return fail(err)
		}
		tf.byURI[a.path] = a.File
	}
	return blocks, tf, cleanup, nil
}

func fileURI(path string) string { return plat.FileURI(path) }

// blockLen is the length of a block as JSON, with the comma that separates it.
func blockLen(b acp.ContentBlock) int {
	j, _ := json.Marshal(b)
	return len(j) + 1
}

func blocksLen(bs []acp.ContentBlock) int {
	n := 0
	for _, b := range bs {
		n += blockLen(b)
	}
	return n
}

// embedBudget is how many bytes of file content (as JSON) one prompt embeds at most; maxPrompt
// bounds the prompt's blocks as JSON, promptOverhead is the room kept for the JSON-RPC envelope.
var embedBudget = 4 << 20

const (
	maxPrompt      = 9 << 20
	promptOverhead = 64 << 10
)

// jsonLen is the length of text as a JSON string.
func jsonLen(text []byte) int {
	b, _ := json.Marshal(string(text))
	return len(b)
}

// openTurns makes the node's directory for turn copies, removing what a stopped node left there.
// It is the store file's own (store.db.turns), and the store's lock is held: no other node uses it.
//
// What is removed is only what acpost made: the directory carries a mark file, and only the turn
// directories in it (msg_…) go. A path of that name that is not such a directory stops the start.
func (n *Node) openTurns() error {
	if p := n.st.Path(); p != "" {
		dir, err := filepath.Abs(p + ".turns") // agents get file:// links into it, from any cwd
		if err != nil {
			return err
		}
		n.turns = dir
		mark := filepath.Join(dir, turnsMark)
		st, err := os.Lstat(dir)
		switch {
		case os.IsNotExist(err):
			if err := os.Mkdir(dir, 0o700); err != nil {
				return err
			}
			return os.WriteFile(mark, nil, 0o600)
		case err != nil:
			return err
		case !st.IsDir():
			return fmt.Errorf("%s exists and is not acpost's directory of turn copies: move it away", dir)
		}
		if _, err := os.Lstat(mark); err != nil {
			return fmt.Errorf("%s exists and is not acpost's directory of turn copies (no %s in it): move it away", dir, turnsMark)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "msg_") {
				if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
					return err
				}
			}
		}
		return nil
	}
	dir, err := os.MkdirTemp("", "acpost-turns-")
	n.turns = dir
	return err
}

// turnsMark marks a directory of turn copies as acpost's.
const turnsMark = ".acpost-turns"

func mediaType(t string) string {
	mt, _, _ := mime.ParseMediaType(t)
	return mt
}

// isImage says whether agents take the type as an image block (the formats models read).
func isImage(t string) bool {
	switch mediaType(t) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// A file:// link opens only on the machine that has the file: it does not leave this node. The hint
// names how the command at hand sends a copy.
const (
	sendCopyHint   = "a file:// link opens only on the machine that has the file; send a copy: --attach FILE instead of --link"
	attachCopyHint = "a file:// link opens only on the machine that has the file; attach a copy: `acpost attach FILE`, without --link"
)

// isFileLink says whether uri is a file:// link, by its parsed scheme (FILE:// is one too).
func isFileLink(uri string) bool {
	u, err := url.Parse(uri)
	return err == nil && strings.EqualFold(u.Scheme, "file")
}

// localLink is the first file:// link of files.
func localLink(files []store.File) (store.File, bool) {
	for _, f := range files {
		if f.URI != "" && isFileLink(f.URI) {
			return f, true
		}
	}
	return store.File{}, false
}

// noLocalLinks refuses file:// links for a message that crosses to another node (where: who is
// there).
func noLocalLinks(files []store.File, where, hint string) error {
	if f, ok := localLink(files); ok {
		return errh("invalid", hint, "%s is a link to a file on this machine (%s), and %s is on another node", f.Name, f.URI, where)
	}
	return nil
}

// senderElsewhere says whether m's sender is on another node: it came through the hub, or (on the
// hub) a joined node sent it.
func (n *Node) senderElsewhere(m store.Message) bool {
	if strings.HasPrefix(m.Origin, originHub) {
		return true
	}
	from, err := ParseAddress(m.From)
	return err != nil || (from.Node != "" && from.Node != n.Name())
}

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
		size += int64(f.Size)
		earlier[f.Name] = true
	}
	for i := range files {
		f := &files[i]
		if err := validFileName(f.Name); err != nil {
			return err
		}
		switch {
		case now[f.Name]:
			return errh("conflict", "files go by their base name: rename one, or attach an archive", "two of the files are named %s", f.Name)
		case earlier[f.Name]:
			return errh("conflict", "rename the file; what is attached stays", "%s already has a file %s (attached earlier)", side, f.Name)
		}
		now[f.Name] = true
		size += int64(len(f.Data))
		f.Type = fileType(f.Name, f.Type, f.Data)
		f.Size = len(f.Data)
		f.Reply = false
	}
	if size > int64(lim.MaxFilesSize) {
		return errf("refused", "the files of %s are %d bytes together, over limits.max_files_size (%d)", side, size, lim.MaxFilesSize)
	}
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
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return http.DetectContentType(data)
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
		size += int64(f.Size)
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

// attached is a message's file put on disk for an agent's turn.
type attached struct {
	store.File
	path string
}

// promptBlocks is the prompt of a turn: the text, then each attached file as the agent takes it —
// text embedded, an image inline, anything else (or for an agent that takes neither) a link to
// its copy on disk. The copies go when the turn ends (cleanup).
func (s *acpSlot) promptBlocks(ctx context.Context, m store.Message, caps acp.PromptCapabilities) ([]acp.ContentBlock, func(), error) {
	nothing := func() {}
	if len(m.Files) == 0 && jsonLen([]byte(m.Body)) <= maxPrompt-promptOverhead {
		return []acp.ContentBlock{acp.TextBlock(s.prompt(m, nil))}, nothing, nil
	}
	var all []store.File
	if len(m.Files) > 0 {
		var err error
		if all, err = s.a.n.st.FilesOf(ctx, m.ID); err != nil {
			return nil, nothing, err
		}
	}
	dir, err := os.MkdirTemp(s.a.n.turns, m.ID+"-")
	if err != nil {
		return nil, nothing, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	fail := func(err error) ([]acp.ContentBlock, func(), error) { cleanup(); return nil, nothing, err }
	var files []attached
	for i, f := range pick(all, false) {
		// A directory per file: names that differ only in case are two files on any file system.
		sub := filepath.Join(dir, strconv.Itoa(i))
		if err := os.Mkdir(sub, 0o700); err != nil {
			return fail(err)
		}
		path := filepath.Join(sub, f.Name)
		if err := os.WriteFile(path, f.Data, 0o600); err != nil {
			return fail(err)
		}
		files = append(files, attached{File: f, path: path})
	}

	// The request stays inside the frame an agent reads (the Go SDK's is 10 MiB): its size is
	// measured as the JSON it is. Every file starts as a link; then, file by file, what the agent
	// takes is embedded while it fits.
	links := make([]acp.ContentBlock, len(files))
	for i, f := range files {
		link := acp.ResourceLinkBlock(f.Name, fileURI(f.path))
		typ, size := f.Type, f.Size
		link.ResourceLink.MimeType, link.ResourceLink.Size = &typ, &size
		links[i] = link
	}
	text := s.prompt(m, files)
	used := blockLen(acp.TextBlock(text)) + blocksLen(links)
	if used > maxPrompt-promptOverhead {
		// The body is what does not fit: it goes as a file the prompt points to.
		path := filepath.Join(dir, "message.txt")
		if err := os.WriteFile(path, []byte(m.Body), 0o600); err != nil {
			return fail(err)
		}
		m.Body = fmt.Sprintf("[acpost] The message is %d bytes, too large to send in the prompt: it is in %s (until this turn ends). Read it there; it is the task.", len(m.Body), path)
		text = s.prompt(m, files)
		used = blockLen(acp.TextBlock(text)) + blocksLen(links)
		if used > maxPrompt-promptOverhead {
			return fail(fmt.Errorf("the names and paths of %d files alone are %d bytes, over what one prompt carries (%d): send fewer files", len(files), used, maxPrompt))
		}
	}
	blocks := []acp.ContentBlock{acp.TextBlock(text)}
	embedded := 0
	for i, f := range files {
		var embed *acp.ContentBlock
		switch {
		case caps.EmbeddedContext && len(f.Data) <= embedBudget-embedded && isText(f.File):
			typ := f.Type
			b := acp.ResourceBlock(acp.EmbeddedResourceResource{
				TextResourceContents: &acp.TextResourceContents{Uri: fileURI(f.path), Text: string(f.Data), MimeType: &typ}})
			embed = &b
		case caps.Image && isImage(f.Type) && base64.StdEncoding.EncodedLen(len(f.Data)) <= embedBudget-embedded:
			b := acp.ImageBlock(base64.StdEncoding.EncodeToString(f.Data), mediaType(f.Type))
			embed = &b
		}
		if embed != nil {
			n, linkN := blockLen(*embed), blockLen(links[i])
			if used-linkN+n <= maxPrompt-promptOverhead && embedded+n <= embedBudget {
				blocks = append(blocks, *embed)
				used, embedded = used-linkN+n, embedded+n
				continue
			}
		}
		blocks = append(blocks, links[i])
	}
	return blocks, cleanup, nil
}

func fileURI(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }

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

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// readFiles reads the files to attach; each goes by its base name. The node's limits are checked
// first, so a file it would refuse is not read into memory.
func readFiles(ctx context.Context, c *client.Client, paths []string) ([]store.File, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	bad := func(err error, hint string) error {
		return &exitErr{code: exitUsage, name: "invalid", err: err, hint: hint}
	}
	st, err := c.Status(ctx)
	if err != nil {
		return nil, err // nothing could be sent anyway: say why before reading anything
	}
	lim := st.Limits
	if lim == nil {
		lim = &client.Limits{MaxFiles: store.MaxFilesPerRequest / 2, MaxFilesSize: 16 << 20} // a node older than its limits in status
	}
	refused := func(format string, args ...any) error {
		return &exitErr{code: exitRefused, name: "refused", err: fmt.Errorf(format, args...),
			hint: "send fewer or smaller files (an archive, a link), or raise the node's limits"}
	}
	if len(paths) > lim.MaxFiles {
		return nil, refused("%d files, over the node's limits.max_files (%d)", len(paths), lim.MaxFiles)
	}
	var out []store.File
	from := map[string]string{}
	left := lim.MaxFilesSize
	for _, p := range paths {
		name := filepath.Base(p)
		if prev, dup := from[name]; dup {
			return nil, bad(fmt.Errorf("%s and %s would both go as %s", prev, p, name), "files go by their base name: rename one, or attach an archive")
		}
		from[name] = p
		data, err := readFile(p, left)
		var over *overLimit
		switch {
		case errors.As(err, &over):
			return nil, refused("the files are %d bytes or more together, over the node's limits.max_files_size (%d)", lim.MaxFilesSize-left+over.size, lim.MaxFilesSize)
		case errors.Is(err, errNotFile):
			return nil, bad(fmt.Errorf("%s is not a file", p), "attach the files of a directory one by one, or an archive of it")
		case errors.Is(err, errChanged):
			return nil, bad(fmt.Errorf("%s changed while it was read", p), "attach it again once nothing writes to it")
		case err != nil:
			return nil, bad(err, "the path is read by this process, from its working directory")
		}
		left -= int64(len(data))
		out = append(out, store.File{Name: name, Data: data})
	}
	return out, nil
}

var (
	errNotFile = errors.New("not a file")
	errChanged = errors.New("changed while read")
)

type overLimit struct{ size int64 }

func (o *overLimit) Error() string { return fmt.Sprintf("%d bytes, over the limit", o.size) }

// readFile reads a regular file of at most max bytes. The size is the open file's, and a file that
// grows or shrinks while it is read is refused rather than sent cut.
func readFile(path string, max int64) ([]byte, error) {
	// Non-blocking: a FIFO or a device opens at once and is refused below, rather than waiting.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errNotFile
	}
	if st.Size() > max {
		return nil, &overLimit{st.Size()}
	}
	data, err := io.ReadAll(io.LimitReader(f, st.Size()+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != st.Size() {
		return nil, errChanged
	}
	return data, nil
}

type savedFile struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  int    `json:"size"`
	Reply bool   `json:"reply,omitempty"`
	Path  string `json:"path,omitempty"` // where it was saved
	URI   string `json:"uri,omitempty"`  // a link: where it is, nothing saved
}

// where is the saved path, or the link.
func (s savedFile) where() string {
	if s.URI != "" {
		return "link " + s.Name + ": " + s.URI
	}
	return s.Path
}

// describe is a file as a line shows it.
func describe(f store.File) string {
	if f.Link() {
		return fmt.Sprintf("%s (%s) → %s", f.Name, f.Type, f.URI)
	}
	return fmt.Sprintf("%s (%s, %d bytes)", f.Name, f.Type, f.Size)
}

// readLinks makes links of paths (this machine's files, absolute) and URLs: nothing is read or copied.
func readLinks(targets []string) ([]store.File, error) {
	var out []store.File
	bad := func(err error, hint string) error {
		return &exitErr{code: exitUsage, name: "invalid", err: err, hint: hint}
	}
	for _, t := range targets {
		f := store.File{}
		if u, err := url.Parse(t); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			f.URI, f.Name = t, path.Base(u.Path)
			if f.Name == "/" || f.Name == "." || f.Name == "" {
				f.Name = u.Host
			}
		} else {
			if u, err := url.Parse(t); err == nil && u.Scheme == "file" {
				// file:///path (any case of the scheme) is the path; a file on another host is not here.
				if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
					return nil, bad(fmt.Errorf("%s names a file on %s, not on this machine", t, u.Host), "--link takes a file of this machine or an http(s) URL")
				}
				t = u.Path
			}
			abs, err := filepath.Abs(t)
			if err != nil {
				return nil, err
			}
			st, err := os.Stat(abs)
			if err != nil {
				return nil, bad(err, "--link takes a file of this machine or an http(s) URL")
			}
			if !st.Mode().IsRegular() {
				return nil, bad(fmt.Errorf("%s is not a file", t), "link the files of a directory one by one")
			}
			f.URI, f.Name, f.Size = (&url.URL{Scheme: "file", Path: abs}).String(), filepath.Base(abs), int(st.Size())
		}
		out = append(out, f)
	}
	return out, nil
}

// saveFiles writes files into dir, refusing to overwrite unless force; exists is the hint then.
// The directory is held open (os.Root): nothing is written outside it, whatever its names or
// symlinks are, or become, meanwhile.
func saveFiles(files []store.File, dir string, force bool, exists string) ([]savedFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var out []savedFile
	for _, f := range files {
		if f.Link() {
			// A link is kept where it is: said, not copied.
			out = append(out, savedFile{Name: f.Name, Type: f.Type, Size: f.Size, Reply: f.Reply, URI: f.URI})
			continue
		}
		// A name from a node is a base name or nothing is written.
		if f.Name == "" || f.Name == "." || f.Name == ".." || strings.ContainsAny(f.Name, "/\\\x00") {
			return out, &exitErr{code: exitError, name: "invalid", err: fmt.Errorf("refusing to save a file named %q", f.Name)}
		}
		path := filepath.Join(dir, f.Name)
		if err := writeIn(root, f.Name, f.Data, force); errors.Is(err, os.ErrExist) {
			return out, &exitErr{code: exitError, name: "exists", err: fmt.Errorf("%s exists", path), hint: exists}
		} else if err != nil {
			return out, err
		}
		out = append(out, savedFile{Name: f.Name, Type: f.Type, Size: f.Size, Reply: f.Reply, Path: path})
	}
	return out, nil
}

// writeIn writes name in root: a new file (O_EXCL: an existing name, a symlink too, is refused), or
// with force a new file renamed over the old one, which keeps the old one's mode; a symlink is
// replaced, its target never written.
func writeIn(root *os.Root, name string, data []byte, force bool) error {
	if !force {
		w, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return errors.Join(err, w.Close())
	}
	mode := os.FileMode(0o644) // and the umask, applied by the system
	keep := false
	if st, err := root.Lstat(name); err == nil && st.Mode().IsRegular() {
		mode, keep = st.Mode().Perm(), true
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	tmp := ".acpost-" + hex.EncodeToString(b[:])
	w, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	err = errors.Join(err, w.Close())
	if err == nil && keep {
		err = root.Chmod(tmp, mode) // the old file's mode, not narrowed by the umask
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp)
	}
	return err
}

// noteFiles tells, on stderr, which files a message or its reply carries and how to get them.
func noteFiles(id string, files []store.File, reply bool) {
	var names []string
	for _, f := range files {
		if f.Reply == reply {
			names = append(names, describe(f))
		}
	}
	if len(names) == 0 {
		return
	}
	what, cmd := "attached", "acpost files "+id
	if reply {
		what, cmd = "attached to the reply", cmd+" --reply"
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n  `%s` saves them here\n", what, strings.Join(names, ", "), cmd)
}

func filesCmd(g *globals) *cobra.Command {
	var dir string
	var force, toStdout, reply bool
	cmd := &cobra.Command{
		Use:   "files ID [NAME...] [--reply]",
		Short: "Save the files attached to a message (--reply: to its reply)",
		Example: `  acpost files msg_… -o ./in               # the message's files, into ./in
  acpost files msg_… --reply                 # what the agent attached to its answer
  acpost files msg_… report.md --reply --stdout`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			files, err := c.Files(ctx, args[0])
			if err != nil {
				return err
			}
			side, otherSide, flag := "the message", "its reply", " --reply"
			if reply {
				side, otherSide, flag = "the reply", "the message itself", ""
			}
			n := 0
			for _, f := range files {
				if f.Reply != reply {
					n++
				}
			}
			other := otherSide + " has no files either"
			if n > 0 {
				other = fmt.Sprintf("%s has %d: `acpost files %s%s`", otherSide, n, args[0], flag)
			}
			names, found := args[1:], map[string]bool{}
			var picked []store.File
			for _, f := range files {
				if f.Reply == reply && (len(names) == 0 || slices.Contains(names, f.Name)) {
					picked = append(picked, f)
					found[f.Name] = true
				}
			}
			for _, n := range names {
				if !found[n] {
					return &exitErr{code: exitNotFound, name: "not_found", err: fmt.Errorf("%s of %s has no file %s", side, args[0], n),
						hint: "`acpost show " + args[0] + "` lists its files; " + other}
				}
			}
			if len(picked) == 0 {
				return &exitErr{code: exitNotFound, name: "not_found", err: fmt.Errorf("%s of %s has no files", side, args[0]), hint: other}
			}
			if toStdout {
				if len(picked) != 1 {
					return usageErr(errors.New("--stdout takes one file: name it"))
				}
				if picked[0].Link() {
					return &exitErr{code: exitError, name: "link", err: fmt.Errorf("%s is a link, kept where it is: %s", picked[0].Name, picked[0].URI)}
				}
				_, err := os.Stdout.Write(picked[0].Data)
				return err
			}
			out, err := saveFiles(picked, dir, force, "--force overwrites; -o DIR saves elsewhere")
			for _, s := range out {
				if g.json {
					printJSON(s)
				} else {
					fmt.Println(s.where())
				}
			}
			return err
		},
	}
	cmd.Flags().StringVarP(&dir, "out", "o", ".", "directory to save into")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite files that exist")
	cmd.Flags().BoolVar(&toStdout, "stdout", false, "write the one named file to stdout")
	cmd.Flags().BoolVar(&reply, "reply", false, "the files attached to the reply")
	return cmd
}

func attachCmd(g *globals) *cobra.Command {
	var link bool
	cmd := &cobra.Command{
		Use:   "attach FILE... [--link]",
		Short: "Inside an agent's turn: attach files to the reply it is writing",
		Long: "An ACP agent's reply is its final answer in the turn; files it attaches with this command go with\n" +
			"that answer. With --link the files are kept where they are (a path of this machine, or an http(s) URL):\n" +
			"the reply names them, nothing is copied. (To answer a message taken with recv, use reply --attach.)",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			var files []store.File
			if link {
				files, err = readLinks(args)
			} else {
				files, err = readFiles(cmd.Context(), c, args)
			}
			if err != nil {
				return err
			}
			id, err := c.Attach(cmd.Context(), files)
			if err != nil {
				return err
			}
			if g.json {
				printJSON(map[string]any{"id": id, "attached": len(files)})
			} else {
				fmt.Printf("attached %d file(s) to the reply to %s\n", len(files), id)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&link, "link", false, "attach links to the files (or URLs), not copies")
	return cmd
}

// saveReplyFiles saves the files of m's reply into dir.
func saveReplyFiles(ctx context.Context, c *client.Client, m store.Message, dir string) ([]savedFile, error) {
	has := false
	for _, f := range m.Files {
		has = has || f.Reply
	}
	if !has {
		return nil, nil
	}
	all, err := c.Files(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	var reply []store.File
	for _, f := range all {
		if f.Reply {
			reply = append(reply, f)
		}
	}
	return saveFiles(reply, dir, false, "--save names another directory; `acpost files "+m.ID+" --reply -o DIR --force` overwrites")
}

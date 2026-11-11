package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// What a turn changed, where its agent works in a git repository: the working tree is snapshot
// before and after the turn, through an index of its own (the repository's index, the working
// tree and its refs are not touched; .gitignore is followed), and when the two trees differ the
// node keeps the files and the diff, and says what changed in a "changes" event (which crosses
// the hub like the others; the diff itself stays here).

const (
	gitTimeout     = 20 * time.Second // a snapshot or a diff takes at most this
	changesDiffMax = 1 << 20          // the diff of a turn kept, at most
	changesFiles   = 100              // files listed in the event
	snapshotFiles  = 10000            // untracked files a snapshot takes at most
	snapshotBytes  = 64 << 20         // and their bytes
	snapshotIndex  = 64 << 20         // a repository whose index is bigger is not snapshot (each turn would walk it twice)
	gitStderrMax   = 64 << 10         // what of git's stderr is kept for its error
)

// gitSnap is a working tree as it was.
type gitSnap struct {
	repo, tree, branch string
	at                 time.Time
}

// git runs git in dir with env added; its output, at most max bytes (cut says it went on).
//
// The repository is the agent's, and so is its configuration: nothing it names is run with the
// node's rights. Hooks and fsmonitor are off, every filter driver the configuration declares is a
// pass-through (gitDefuse), diffs are without textconv. Past gitTimeout the whole process group
// is killed and its pipes let go.
func git(ctx context.Context, dir string, env []string, max int, args ...string) (out []byte, cut bool, err error) {
	return gitDefused(ctx, dir, gitDefuse(ctx, dir), env, max, args...)
}

// gitDefused is git with the repository's filter drivers already read (gitDefuse): a snapshot or a
// diff reads them once for its few calls, not once a call. nil is for a call that runs no filter
// (rev-parse, symbolic-ref).
func gitDefused(ctx context.Context, dir string, defused [][2]string, env []string, max int, args ...string) (out []byte, cut bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), append(gitSafeEnv(defused), env...)...)
	killGroupOnCancel(cmd)
	cmd.WaitDelay = 2 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	stderr := &capped{max: gitStderrMax} // a warning per file of a big add is not kept whole
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	out, _ = io.ReadAll(io.LimitReader(stdout, int64(max)+1))
	if len(out) > max {
		out, cut = out[:max], true
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := cmd.Wait(); err != nil {
		return out, cut, errors.New(strings.TrimSpace(stderr.String() + " " + err.Error()))
	}
	return out, cut, nil
}

// snapshot is dir's working tree as it is now, when dir is in a git repository (else nil).
func snapshot(ctx context.Context, dir string) *gitSnap {
	// The repository and its index in one call; rev-parse runs nothing of the repository.
	paths, _, err := gitDefused(ctx, dir, nil, nil, 16384, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-path", "index")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(paths), "\n"), "\n")
	if len(lines) != 2 {
		return nil
	}
	repo, index := lines[0], lines[1]
	if fi, err := os.Stat(index); err == nil && fi.Size() > snapshotIndex {
		return nil
	}
	defused := gitDefuse(ctx, repo)
	if !untrackedFits(ctx, repo, defused) {
		return nil // too much untracked to take into the repository's objects
	}
	tmp, err := os.CreateTemp("", "acpost-index-")
	if err != nil {
		return nil
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	defer os.Remove(tmp.Name() + ".lock")
	// The repository's index as a start: files unchanged since it are not read again.
	if b, err := os.ReadFile(index); err == nil {
		_ = os.WriteFile(tmp.Name(), b, 0o600)
		// The copy keeps the index's time: git takes an entry as clean by its stat only when the
		// file is older than the index ("racy git"). A copy written now would make a file changed
		// in the index's second, at the same size, look unchanged.
		if fi, err := os.Stat(index); err == nil {
			_ = os.Chtimes(tmp.Name(), fi.ModTime(), fi.ModTime())
		}
	} else {
		os.Remove(tmp.Name())
	}
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	if _, _, err := gitDefused(ctx, repo, defused, env, 1<<16, "add", "-A", "--", "."); err != nil {
		return nil
	}
	tree, _, err := gitDefused(ctx, repo, defused, env, 4096, "write-tree")
	if err != nil {
		return nil
	}
	snap := &gitSnap{repo: repo, tree: strings.TrimSpace(string(tree)), at: time.Now().UTC()}
	if b, _, err := gitDefused(ctx, repo, nil, nil, 4096, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		snap.branch = strings.TrimSpace(string(b))
	}
	return snap
}

// diffSnaps is what changed from one snapshot to the other: the files and the diff.
func diffSnaps(ctx context.Context, before, after *gitSnap) ([]store.ChangedFile, string, bool, error) {
	defused := gitDefuse(ctx, before.repo)
	status, _, err := gitDefused(ctx, before.repo, defused, nil, 1<<20, "diff", "--no-ext-diff", "--no-textconv", "--submodule=short", "-M", "--name-status", "-z", before.tree, after.tree)
	if err != nil {
		return nil, "", false, err
	}
	files := parseNameStatus(status)
	if num, _, err := gitDefused(ctx, before.repo, defused, nil, 1<<20, "diff", "--no-ext-diff", "--no-textconv", "--submodule=short", "-M", "--numstat", "-z", before.tree, after.tree); err == nil {
		addNumstat(files, num)
	}
	// Whole files only, masked, then cut between files: a cut does not leave half a secret.
	diff, cut, err := gitDefused(ctx, before.repo, defused, nil, 2*changesDiffMax, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--submodule=short", "-M", before.tree, after.tree)
	if err != nil {
		return files, "", false, err
	}
	text := string(diff)
	if cut {
		text = wholeFiles(text, len(text))
	}
	text = redact(text)
	if len(text) > changesDiffMax {
		cut, text = true, wholeFiles(text, changesDiffMax)
	}
	return files, text, cut, nil
}

// parseNameStatus reads `git diff --name-status -z`: status, then a path (two for a rename or copy).
func parseNameStatus(b []byte) []store.ChangedFile {
	parts := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
	var out []store.ChangedFile
	for i := 0; i < len(parts); i++ {
		st := parts[i]
		if st == "" || i+1 >= len(parts) {
			break
		}
		f := store.ChangedFile{Status: st[:1]}
		if st[0] == 'R' || st[0] == 'C' {
			if i+2 >= len(parts) {
				break
			}
			f.Path = parts[i+2]
			i += 2
		} else {
			f.Path = parts[i+1]
			i++
		}
		out = append(out, f)
	}
	return out
}

// addNumstat adds the lines of `git diff --numstat -z` to the files ("-" for a binary file).
func addNumstat(files []store.ChangedFile, b []byte) {
	idx := map[string]int{}
	for i, f := range files {
		idx[f.Path] = i
	}
	parts := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
	for i := 0; i < len(parts); i++ {
		fields := strings.SplitN(parts[i], "\t", 3)
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		if path == "" && i+2 < len(parts) { // a rename: the old path, then the new
			path = parts[i+2]
			i += 2
		}
		j, ok := idx[path]
		if !ok {
			continue
		}
		if fields[0] == "-" {
			files[j].Added, files[j].Removed = -1, -1
			continue
		}
		files[j].Added, _ = strconv.Atoi(fields[0])
		files[j].Removed, _ = strconv.Atoi(fields[1])
	}
}

// recordChanges keeps what a turn of m changed, and says so in an event: in its git repository,
// what differs between the snapshots; outside one (the agent's directory is not a repository, or
// the file is elsewhere), the edits its tool calls reported (more: some were not kept).
func (n *Node) recordChanges(m store.Message, agent string, before *gitSnap, dir string, started time.Time, edits []fileEdit, more bool) {
	ctx := n.ctx
	repo := ""
	if before != nil {
		repo = before.repo
		n.recordGit(ctx, m, agent, before, dir)
	} else if len(edits) > 0 {
		// No snapshot is not no repository (one too big to snapshot, git failing): what is in a
		// repository is told by its snapshots or not at all (its ignored files are not shown).
		repo = repoRoot(ctx, dir)
	}
	files, diff, cut := reportedChanges(dir, repo, edits)
	if len(files) == 0 {
		return
	}
	c := store.TurnChanges{Msg: m.ID, Agent: agent, Start: started, At: time.Now().UTC(), Repo: dir, Files: files, Diff: diff, Cut: cut || more}
	if err := n.st.AddChanges(ctx, c); err != nil {
		n.log.Debug("changes", "id", m.ID, "err", err)
		return
	}
	n.tellChanges(ctx, m.ID, agent, dir, "", "", "", files)
}

// repoRoot is the git repository dir is in ("" for none).
func repoRoot(ctx context.Context, dir string) string {
	b, _, err := gitDefused(ctx, dir, nil, nil, 16384, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// recordGit keeps what differs in the turn's repository between its snapshots.
func (n *Node) recordGit(ctx context.Context, m store.Message, agent string, before *gitSnap, dir string) {
	after := snapshot(ctx, dir)
	if after == nil || after.repo != before.repo || after.tree == before.tree {
		return
	}
	files, diff, cut, err := diffSnaps(ctx, before, after)
	if err != nil {
		n.log.Debug("changes", "id", m.ID, "err", err)
		return
	}
	c := store.TurnChanges{Msg: m.ID, Agent: agent, Start: before.at, At: after.at, Repo: before.repo, Branch: after.branch,
		Before: before.tree, After: after.tree, Files: files, Diff: diff, Cut: cut}
	if err := n.st.AddChanges(ctx, c); err != nil {
		n.log.Debug("changes", "id", m.ID, "err", err)
		return
	}
	n.tellChanges(ctx, m.ID, agent, before.repo, after.branch, before.tree, after.tree, files)
}

// tellChanges says what a turn changed in an event (the files; the diff stays on the node).
func (n *Node) tellChanges(ctx context.Context, id, agent, repo, branch, before, after string, files []store.ChangedFile) {
	ev := struct {
		Agent  string              `json:"agent"`
		Repo   string              `json:"repo"`
		Branch string              `json:"branch,omitempty"`
		Before string              `json:"before"`
		After  string              `json:"after"`
		Files  []store.ChangedFile `json:"files"`
		More   int                 `json:"more,omitempty"`
	}{Agent: agent, Repo: filepath.Base(repo), Branch: branch, Before: before, After: after, Files: files}
	if len(ev.Files) > changesFiles {
		ev.Files, ev.More = ev.Files[:changesFiles], len(files)-changesFiles
	}
	for len(eventJSON(ev)) > toolEventMax && len(ev.Files) > 1 {
		ev.More += len(ev.Files) / 2
		ev.Files = ev.Files[:len(ev.Files)-len(ev.Files)/2]
	}
	_ = n.addEvent(ctx, id, "changes", eventJSON(ev), false)
}

// reportedChanges is the edits a turn's tool calls reported as changes: the files (their paths
// from dir, or whole when elsewhere) and a diff of each edit, secrets masked. Files inside repo
// (where their links lead) are left out: its snapshots tell those, whoever changed them. A turn
// without a repository has only these, so what a command changed (not an edit of the agent's) is
// not among them. cut: some edits have no diff here (too long to keep).
func reportedChanges(dir, repo string, edits []fileEdit) ([]store.ChangedFile, string, bool) {
	var files []store.ChangedFile
	at := map[string]int{}
	var diff strings.Builder
	cut := false
	realRepo := repo
	if repo != "" {
		if r, err := filepath.EvalSymlinks(repo); err == nil {
			realRepo = r
		}
	}
	for _, e := range edits {
		if e.path == "" {
			continue
		}
		abs := e.path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(dir, abs)
		}
		abs = filepath.Clean(abs)
		// Where the file is, its links followed: a link in the repository to a file outside it is
		// that file (the snapshots have the link, unchanged), one outside to a file in it the
		// repository's.
		if repo != "" && inside(realRepo, realPath(abs)) {
			continue
		}
		if !e.cut && e.old == e.new && !e.created && !e.deleted {
			continue // nothing changed
		}
		p := abs
		if rel, err := filepath.Rel(dir, abs); err == nil && inside(dir, abs) {
			p = rel
		}
		p = filepath.ToSlash(p)
		hunk, added, removed := "", e.added, e.removed
		if !e.cut {
			hunk, added, removed = unifiedHunk(e.old, e.new)
		}
		i, ok := at[p]
		if !ok {
			i = len(files)
			at[p] = i
			files = append(files, store.ChangedFile{Path: p, Status: "M"})
		}
		// What became of the file over the turn: made (and edited since), removed, or changed.
		switch {
		case e.deleted:
			files[i].Status = "D"
		case e.created:
			files[i].Status = "A"
		case files[i].Status == "D":
			files[i].Status = "M" // removed, then written again
		}
		files[i].Added += added
		files[i].Removed += removed
		if e.cut { // too long to keep: listed, without its diff
			cut = true
			continue
		}
		// As git writes them: a file made empty has its header alone.
		qa, qb := gitQuote("a/"+p), gitQuote("b/"+p)
		section := "diff --git " + qa + " " + qb + "\n"
		switch {
		case e.deleted && hunk == "":
			section += "deleted file mode 100644\n"
		case e.deleted:
			section += "deleted file mode 100644\n--- " + qa + "\n+++ /dev/null\n" + hunk
		case e.created && hunk == "":
			section += "new file mode 100644\n"
		case e.created:
			section += "new file mode 100644\n--- /dev/null\n+++ " + qb + "\n" + hunk
		case hunk == "":
			continue // an edit of a masked value alone, its lines folded: counted, nothing to show
		default:
			section += "--- " + qa + "\n+++ " + qb + "\n" + hunk
		}
		if diff.Len()+len(section) > changesDiffMax {
			cut = true // whole sections only: a cut does not leave half a masked secret
			continue
		}
		diff.WriteString(section)
	}
	return files, diff.String(), cut
}

// gitQuote is a path as git writes it in a diff's header: as it is, or, when it has a control
// character, a quotation mark or a backslash, in quotation marks with those escaped (C style,
// octal for the rest), so a line break in a name does not break the header.
func gitQuote(p string) string {
	plain := true
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f || c == '"' || c == '\\' {
			plain = false
			break
		}
	}
	if plain {
		return p
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			b.WriteString("\\" + fmt.Sprintf("%03o", c))
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// inside says whether file is dir or in it.
func inside(dir, file string) bool {
	rel, err := filepath.Rel(dir, file)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// realPath is file where its links lead: the file's own when it is there, else its directory's
// (a file since removed, or not yet made).
func realPath(file string) string {
	if p, err := filepath.EvalSymlinks(file); err == nil {
		return p
	}
	d, err := filepath.EvalSymlinks(filepath.Dir(file))
	if err != nil {
		return file
	}
	return filepath.Join(d, filepath.Base(file))
}

// noEOL marks, in a text's lines, a last line that has no line end: it differs from the same line
// ended (a diff says so: "\ No newline at end of file").
const noEOL = "\x00"

// maskedLine stands for a changed line of a masked value that spans lines.
const maskedLine = "(a line of a masked value)"

// endedLines is a text's lines; a last line without a line end carries noEOL.
func endedLines(s string) []string {
	if s == "" {
		return nil
	}
	l := strings.Split(s, "\n")
	if l[len(l)-1] == "" {
		return l[:len(l)-1]
	}
	l[len(l)-1] += noEOL
	return l
}

// unifiedHunk is one hunk of a unified diff from old to new, secrets masked, and the lines it
// adds and removes: the stretch between the lines they start and end with alike, with three lines
// around it ("" when the texts are the same).
//
// What changed is found in the texts as they are, and shown masked: an edit of a secret alone is
// an edit (its lines shown, both masked). When masking moves lines (a value over several lines),
// the masked texts are compared instead, and such an edit of a secret alone has no hunk.
func unifiedHunk(old, new string) (hunk string, added, removed int) {
	a, b := endedLines(old), endedLines(new)
	sa, sb := endedLines(redact(old)), endedLines(redact(new))
	hidden := false
	if len(sa) != len(a) || len(sb) != len(b) {
		// Masking moved lines. The masked texts are compared; when they are the same (the edit is
		// inside a masked value of several lines), where the texts themselves differ is said, its
		// lines not shown.
		if hidden = slices.Equal(sa, sb); !hidden {
			a, b = sa, sb
		}
	}
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	if p == len(a) && p == len(b) {
		return "", 0, 0
	}
	if hidden {
		na, nb := len(a)-s-p, len(b)-s-p
		at := func(n int) string {
			if n == 0 {
				return strconv.Itoa(p) + ",0"
			}
			return strconv.Itoa(p+1) + "," + strconv.Itoa(n)
		}
		return "@@ -" + at(na) + " +" + at(nb) + " @@\n" + strings.Repeat("-"+maskedLine+"\n", na) + strings.Repeat("+"+maskedLine+"\n", nb), nb, na
	}
	const around = 3
	from := max(p-around, 0)
	tail := min(s, around)
	na, nb := len(a)-s+tail-from, len(b)-s+tail-from
	start := func(n int) int {
		if n == 0 {
			return from
		}
		return from + 1
	}
	var w strings.Builder
	w.WriteString("@@ -" + strconv.Itoa(start(na)) + "," + strconv.Itoa(na) + " +" + strconv.Itoa(start(nb)) + "," + strconv.Itoa(nb) + " @@\n")
	line := func(op, l string) {
		if t, ok := strings.CutSuffix(l, noEOL); ok {
			w.WriteString(op + t + "\n\\ No newline at end of file\n")
			return
		}
		w.WriteString(op + l + "\n")
	}
	for _, l := range sa[from:p] {
		line(" ", l)
	}
	for _, l := range sa[p : len(a)-s] {
		line("-", l)
	}
	for _, l := range sb[p : len(b)-s] {
		line("+", l)
	}
	for _, l := range sa[len(a)-s : len(a)-s+tail] {
		line(" ", l)
	}
	return w.String(), len(b) - s - p, len(a) - s - p
}

// Changes is what the turns of a message changed, and with tree those of the messages its agent
// sent while working on it (subagents), on this node; with each repository's diff from its first
// snapshot to its last, when the repository still has them.
//
// A diff of a repository is all that changed in it, whoever changed it: only the node's user and
// its agents may read them.
func (n *Node) Changes(ctx context.Context, p Principal, id string, tree, withDiff bool) ([]store.TurnChanges, []store.RepoDiff, error) {
	if !p.local() {
		return nil, nil, errf("refused", "changes are for the node's user")
	}
	if _, err := n.Get(ctx, p, id); err != nil {
		return nil, nil, err
	}
	ids := []string{id}
	if tree {
		all, err := n.st.Descendants(ctx, id, 1000)
		if err != nil {
			return nil, nil, err
		}
		ids = ids[:0]
		for _, d := range all {
			if m, err := n.st.Get(ctx, d); err == nil && n.canSee(p, m) {
				ids = append(ids, d)
			}
		}
	}
	turns, err := n.st.ChangesOf(ctx, ids, withDiff)
	if err != nil {
		return nil, nil, err
	}
	var whole []store.RepoDiff
	if withDiff {
		// From the earliest snapshot before a turn to the latest after one (turns nest: a subagent's
		// ends before its lead's).
		first, last := map[string]*gitSnap{}, map[string]*gitSnap{}
		var repos []string
		reported := map[string]*store.RepoDiff{}
		reportedDiff := map[string]*strings.Builder{}
		var reportedIn []string
		for _, t := range turns {
			if t.Before == "" { // edits the agent reported, outside a repository: no trees to diff
				r := reported[t.Repo]
				if r == nil {
					r = &store.RepoDiff{Repo: t.Repo}
					reported[t.Repo] = r
					reportedIn = append(reportedIn, t.Repo)
				}
				r.Files, r.Cut = joinChanged(r.Files, t.Files), r.Cut || t.Cut
				w := reportedDiff[t.Repo]
				if w == nil {
					w = &strings.Builder{}
					reportedDiff[t.Repo] = w
				}
				// Whole files while they fit: the bound holds as the turns are put together.
				if part := t.Diff; w.Len()+len(part) <= changesDiffMax {
					w.WriteString(part)
				} else if r.Cut = true; w.Len() < changesDiffMax {
					w.WriteString(wholeFiles(part, changesDiffMax-w.Len()))
				}
				continue
			}
			if f := first[t.Repo]; f == nil {
				repos = append(repos, t.Repo)
				first[t.Repo] = &gitSnap{repo: t.Repo, tree: t.Before, at: t.Start}
			} else if t.Start.Before(f.at) {
				f.tree, f.at = t.Before, t.Start
			}
			if l := last[t.Repo]; l == nil || !t.At.Before(l.at) {
				last[t.Repo] = &gitSnap{repo: t.Repo, tree: t.After, branch: t.Branch, at: t.At}
			}
		}
		for _, r := range repos {
			files, diff, cut, err := diffSnaps(ctx, first[r], last[r])
			if err != nil {
				continue // the repository has let the trees go
			}
			whole = append(whole, store.RepoDiff{Repo: r, Branch: last[r].branch, Before: first[r].tree, After: last[r].tree, Files: files, Diff: diff, Cut: cut})
		}
		for _, r := range reportedIn {
			d := reported[r]
			d.Diff = reportedDiff[r].String()
			whole = append(whole, *d)
		}
	}
	return turns, whole, nil
}

// joinChanged adds more to files: a file in both once, its lines summed.
func joinChanged(files, more []store.ChangedFile) []store.ChangedFile {
	at := map[string]int{}
	for i, f := range files {
		at[f.Path] = i
	}
	for _, f := range more {
		if i, ok := at[f.Path]; ok {
			files[i].Added += f.Added
			files[i].Removed += f.Removed
			continue
		}
		at[f.Path] = len(files)
		files = append(files, f)
	}
	return files
}

// gitSafeEnv is the environment git runs in: no prompt, no pager, no external diff, no network
// (no lazy fetch, no protocol, no ssh command), and configuration (given as GIT_CONFIG_KEY_n and
// _VALUE_n: a key is not split at "=" there) that turns off what the repository may name to run:
// hooks, fsmonitor, submodule diffs, and the filters in extra.
func gitSafeEnv(extra [][2]string) []string {
	conf := append([][2]string{
		{"core.hooksPath", "/dev/null"}, {"core.fsmonitor", "false"}, {"core.untrackedCache", "false"},
		{"diff.submodule", "short"}, {"submodule.recurse", "false"}, {"protocol.allow", "never"},
		{"core.sshCommand", "false"}, {"diff.external", ""},
	}, extra...)
	env := []string{"GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_EXTERNAL_DIFF=", "GIT_PAGER=cat",
		"GIT_NO_LAZY_FETCH=1", "GIT_SSH_COMMAND=false", "GIT_ALLOW_PROTOCOL=", "GIT_CONFIG_COUNT=" + strconv.Itoa(len(conf))}
	for i, kv := range conf {
		env = append(env, "GIT_CONFIG_KEY_"+strconv.Itoa(i)+"="+kv[0], "GIT_CONFIG_VALUE_"+strconv.Itoa(i)+"="+kv[1])
	}
	return env
}

// gitDefuse is the configuration that makes every filter driver the repository declares a
// pass-through (clean, smudge and process are commands it would run), not required.
func gitDefuse(ctx context.Context, dir string) [][2]string {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", "--null", "--name-only", "--get-regexp", `^filter\..*\.(clean|smudge|process|required)$`)
	cmd.Env = append(os.Environ(), gitSafeEnv(nil)...)
	killGroupOnCancel(cmd)
	cmd.WaitDelay = 2 * time.Second
	b, _ := cmd.Output()
	seen := map[string]bool{}
	var out [][2]string
	for _, key := range strings.Split(string(b), "\x00") {
		i := strings.LastIndexByte(key, '.')
		if !strings.HasPrefix(key, "filter.") || i < len("filter.") {
			continue
		}
		driver := key[:i] // filter.<name>
		if seen[driver] {
			continue
		}
		seen[driver] = true
		out = append(out, [2]string{driver + ".clean", "cat"}, [2]string{driver + ".smudge", "cat"}, [2]string{driver + ".process", ""}, [2]string{driver + ".required", "false"})
	}
	return out
}

// untrackedFits says whether the files a snapshot would take into repo's object store (untracked,
// and tracked but changed since the index) are few and small enough.
func untrackedFits(ctx context.Context, repo string, defused [][2]string) bool {
	b, cut, err := gitDefused(ctx, repo, defused, nil, 4<<20, "ls-files", "-z", "--others", "--modified", "--exclude-standard")
	if err != nil || cut {
		return false
	}
	var n int
	var size int64
	for _, name := range strings.Split(string(b), "\x00") {
		if name == "" {
			continue
		}
		if n++; n > snapshotFiles {
			return false
		}
		if st, err := os.Lstat(filepath.Join(repo, name)); err == nil {
			if size += st.Size(); size > snapshotBytes {
				return false
			}
		}
	}
	return true
}

// killGroupOnCancel makes cmd a process group of its own, killed whole when its context ends: a
// hook or filter it started does not outlive it.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// wholeFiles is the files of a diff that fit in max bytes, each whole (the last one, cut, goes;
// none if the first does not fit).
func wholeFiles(diff string, max int) string {
	head := diff[:min(len(diff), max)]
	if i := strings.LastIndex(head, "\ndiff --git "); i >= 0 {
		return head[:i+1]
	}
	return ""
}

// capped keeps the first max bytes written to it and drops the rest. The buffer is a field, not
// embedded: an embedded bytes.Buffer brings its ReadFrom, which io.Copy (os/exec's copy of a
// process's output) calls instead of Write, past the cap.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }

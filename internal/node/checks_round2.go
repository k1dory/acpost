package node

import (
	"bytes"
	"strings"

	"github.com/k1dory/acpost/internal/store"
)

// asListed says whether the files the hub sent for one side of a message (got) are those its
// listing announced: the same names, each content file of the listed size and hash, each link to
// the listed place. What a response carries is read tolerantly (a newer hub may add fields), so
// a file whose data came under a name this version does not read arrives empty: this is where
// that, or any other mismatch, is refused rather than kept as the file.
func asListed(listed, got []store.File, side string) error {
	if len(got) != len(listed) {
		return errf("refused", "the hub sent %d of %s's files, its listing has %d", len(got), side, len(listed))
	}
	byName := make(map[string]store.File, len(got))
	for _, f := range got {
		byName[f.Name] = f
	}
	for _, l := range listed {
		f, ok := byName[l.Name]
		switch {
		case !ok:
			return errf("refused", "the hub did not send %s's file %s", side, l.Name)
		case l.Link() || f.Link():
			if f.URI != l.URI {
				return errf("refused", "the hub sent %s's file %s other than listed", side, l.Name)
			}
		case len(f.Data) != l.Size || (l.SHA256 != "" && store.Hash(f.Data) != l.SHA256):
			return errf("refused", "the hub sent %s's file %s other than listed (%d bytes, listed %d)", side, l.Name, len(f.Data), l.Size)
		}
	}
	return nil
}

// What newFilePatch makes of git diff --no-index /dev/null on a new file.
const (
	patchTaken  = iota // the file's whole patch
	patchLeft          // the file is not sent, and named so: too big, or gone or unreadable since listed
	patchFailed        // git did not finish (killed past its time): the handoff fails, not the file
)

// newFilePatch tells a new file's patch from git's failing to read it. The diff exits 1 for a
// difference, and also for a file it cannot find (with nothing on stdout); 128 for one it cannot
// read: a patch is taken only when its output is one (a new file's starts with its header).
func newFilePatch(out []byte, err error) int {
	if CodeOf(err) == "invalid" {
		return patchLeft // over the size a handoff sends
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	exit1 := err == nil || strings.HasSuffix(msg, "exit status 1")
	switch {
	case exit1 && bytes.HasPrefix(out, []byte("diff --git ")) && !strings.Contains(msg, "error:") && !strings.Contains(msg, "fatal:"):
		return patchTaken
	case exit1, strings.HasSuffix(msg, "exit status 128"):
		return patchLeft
	}
	return patchFailed
}

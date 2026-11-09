package node

import (
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

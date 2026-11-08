package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// errRepeat ends Complete's transaction without writing: the call repeats one already recorded.
var errRepeat = errors.New("store: completion already recorded")

// completionKey stands for a completion: the lease it ended under and everything it said (state,
// reply, error code and text, the files by name and content: a type is filled in from those). A repeat is the same key;
// any difference (a file added, another error text) is another answer, and a conflict.
func completionKey(lease string, state State, reply, errCode, errText string, files []File) string {
	h := sha256.New()
	field := func(v string) { fmt.Fprintf(h, "%d:%s;", len(v), v) }
	for _, v := range []string{lease, string(state), reply, errCode, errText} {
		field(v)
	}
	fs := slices.Clone(files)
	slices.SortFunc(fs, func(a, b File) int { return strings.Compare(a.Name, b.Name) })
	for _, f := range fs {
		field(f.Name)
		if f.Link() {
			field("uri " + f.URI)
		} else {
			field("sha256 " + Hash(f.Data))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// completed says whether message id was ended under lease with the completion key.
func completed(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id, lease, key string) (bool, error) {
	if lease == "" {
		return false, nil
	}
	var n int
	err := q.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE id = ? AND completion = ?", id, key).Scan(&n)
	return n > 0, err
}

// RepeatsCompletion says whether Complete with these arguments would repeat the one that ended
// the message: a caller checking a reply against its limits asks first, since a repeat's files are
// attached already and would count twice.
func (s *Store) RepeatsCompletion(ctx context.Context, id, lease string, state State, reply, errCode, errText string, files []File) (bool, error) {
	return completed(ctx, s.r, id, lease, completionKey(lease, state, reply, errCode, errText, files))
}

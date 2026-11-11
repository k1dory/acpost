package main

import (
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// A turn that edited inside a repository and outside it, the repository's snapshots since gone:
// the reported edits had whole do not stand for the repository's stored diff, which is still
// printed turn by turn.
func TestTurnsApartKeepsTheSnapshotTurn(t *testing.T) {
	turns := []store.TurnChanges{
		{Msg: "m", Repo: "/r", Before: "aaa", After: "bbb", Diff: "git"},
		{Msg: "m", Repo: "/r", Diff: "reported"},
	}
	got := turnsApart(turns, []store.RepoDiff{{Repo: "/r", Diff: "reported"}})
	if len(got) != 1 || got[0].Diff != "git" {
		t.Errorf("printed apart: %+v", got)
	}
	if got := turnsApart(turns, []store.RepoDiff{{Repo: "/r", Before: "aaa", After: "bbb"}, {Repo: "/r"}}); len(got) != 0 {
		t.Errorf("both had whole, still apart: %+v", got)
	}
}

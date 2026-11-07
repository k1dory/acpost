package store

import (
	"context"
	"errors"
	"testing"
)

func TestFolders(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	a, err := s.CreateFolder(ctx, "argus")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.CreateFolder(ctx, "  acpost  ")
	if b.Name != "acpost" || b.Pos != a.Pos+1 {
		t.Fatalf("%+v", b)
	}
	if _, err := s.CreateFolder(ctx, "bad\nname"); err == nil {
		t.Fatal("a control character in a name")
	}
	if err := s.MoveChat(ctx, "session:claude/abc", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveChat(ctx, "session:claude/abc", b.ID); err != nil { // moved, not copied
		t.Fatal(err)
	}
	if err := s.MoveChat(ctx, "thread:msg_1", "fld_missing"); !errors.Is(err, ErrNoFolder) {
		t.Fatalf("a missing folder: %v", err)
	}
	if err := s.UpdateFolder(ctx, a.ID, "ARGUS", 5); err != nil {
		t.Fatal(err)
	}
	fs, _ := s.Folders(ctx)
	if len(fs) != 2 || fs[0].ID != b.ID || len(fs[0].Chats) != 1 || fs[1].Name != "ARGUS" || len(fs[1].Chats) != 0 {
		t.Fatalf("%+v", fs)
	}
	if err := s.DeleteFolder(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveChat(ctx, "session:claude/abc", ""); err != nil {
		t.Fatal(err)
	}
	fs, _ = s.Folders(ctx)
	if len(fs) != 1 {
		t.Fatalf("%+v", fs)
	}
	var left int
	s.r.QueryRow(`SELECT count(*) FROM chat_folders`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d chats left in a deleted folder", left)
	}
}

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open("memory:", Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

package store

import (
	"errors"
	"testing"
	"time"
)

// An attach repeated with a link that claims another size is another file under the same name,
// not a repeat.
func TestAttachReplyRepeatComparesLinkSize(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	c, _, _ := s.Claim(ctx, "agent:r", time.Minute)
	lim := FileLimits{Count: 4, Size: 1 << 20}
	link := func(size int) []File {
		return []File{{Name: "build.log", Type: "text/plain", URI: "https://example.com/build.log", Size: size}}
	}
	if err := s.AttachReply(ctx, c.ID, c.LeaseID, link(10), lim); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachReply(ctx, c.ID, c.LeaseID, link(10), lim); err != nil {
		t.Fatalf("the same link again: %v", err)
	}
	if err := s.AttachReply(ctx, c.ID, c.LeaseID, link(11), lim); !errors.Is(err, ErrFileExists) {
		t.Fatalf("the link with another size: %v", err)
	}
}

// Complete's repeat compares the files' types and a link's size too.
func TestCompleteRepeatComparesFileMetadata(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	c, _, _ := s.Claim(ctx, "agent:r", time.Minute)
	f := File{Name: "a.md", Type: "text/markdown", Data: []byte("a")}
	l := File{Name: "b.log", Type: "text/plain", URI: "https://example.com/b.log", Size: 5}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "r", "", "", f, l); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "r", "", "", f, l); err != nil {
		t.Fatalf("the same: %v", err)
	}
	g := f
	g.Type = "text/plain"
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "r", "", "", g, l); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("another type: %v", err)
	}
	m := l
	m.Size = 6
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "r", "", "", f, m); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a link of another size: %v", err)
	}
}

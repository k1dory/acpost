package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// An attach repeated under the same lease (its response was lost) is no error and attaches
// nothing twice; the same name with other content is still refused.
func TestAttachReplyRepeat(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	c, ok, err := s.Claim(ctx, "agent:r", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	lim := FileLimits{Count: 3, Size: 1 << 20}
	f := []File{{Name: "out.txt", Type: "text/plain", Data: []byte("out")}, {Name: "ref", URI: "https://example.com/x"}}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.AttachReply(ctx, c.ID, c.LeaseID, f, lim)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("repeat: %v", err)
		}
	}
	if n, _, _ := s.ReplyFiles(ctx, c.ID); n != 2 {
		t.Fatalf("%d reply files", n)
	}
	if err := s.AttachReply(ctx, c.ID, c.LeaseID, []File{{Name: "out.txt", Type: "text/plain", Data: []byte("other")}}, lim); !errors.Is(err, ErrFileExists) {
		t.Fatalf("same name, other content: %v", err)
	}
}

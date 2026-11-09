//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

type stubNotes struct {
	asked, sent atomic.Int32
	allow       bool
}

func (s *stubNotes) RequestNotificationAuthorization() (bool, error) {
	s.asked.Add(1)
	return s.allow, nil
}

func (s *stubNotes) SendNotification(notifications.NotificationOptions) error {
	s.sent.Add(1)
	return nil
}

// Two messages finishing at once ask for permission once, and race on nothing.
func TestNotifyAsksOnce(t *testing.T) {
	for _, allow := range []bool{true, false} {
		s := &stubNotes{allow: allow}
		n := &Node{notes: s}
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() { defer wg.Done(); n.Notify("done", "a reply") }()
		}
		wg.Wait()
		if s.asked.Load() != 1 {
			t.Fatalf("allow %v: asked %d times", allow, s.asked.Load())
		}
		if want := map[bool]int32{true: 20, false: 0}[allow]; s.sent.Load() != want {
			t.Fatalf("allow %v: %d sent", allow, s.sent.Load())
		}
	}
}

// "~" is the home directory only alone or before a slash: "~foo" is not "<home>foo".
func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{"~": home, "~/src": filepath.Join(home, "src"), "~foo": "~foo", "/a/~b": "/a/~b"} {
		if got := expandHome(in); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

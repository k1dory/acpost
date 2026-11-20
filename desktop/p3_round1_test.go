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

// The test notification is sent whatever the window, and says why when macOS does not allow it.
func TestNotifyTestSaysWhyNot(t *testing.T) {
	ok := &stubNotes{allow: true}
	if err := (&Node{notes: ok}).NotifyTest("acpost", "works"); err != nil || ok.sent.Load() != 1 {
		t.Fatalf("allowed: %v, %d sent", err, ok.sent.Load())
	}
	no := &stubNotes{allow: false}
	if err := (&Node{notes: no}).NotifyTest("acpost", "works"); err == nil || no.sent.Load() != 0 {
		t.Fatalf("refused: %v, %d sent", err, no.sent.Load())
	}
	if err := (&Node{}).NotifyTest("acpost", "works"); err == nil {
		t.Fatal("no notifications: said")
	}
	// Refused, then allowed in System Settings: the test asks again, and so do the next notifications.
	later := &stubNotes{allow: false}
	n := &Node{notes: later}
	n.Notify("done", "x")
	later.allow = true
	if err := n.NotifyTest("acpost", "works"); err != nil || later.sent.Load() != 1 {
		t.Fatalf("allowed since: %v, %d sent", err, later.sent.Load())
	}
	n.Notify("done", "y")
	if later.sent.Load() != 2 {
		t.Fatalf("a notification after the test: %d sent", later.sent.Load())
	}
}

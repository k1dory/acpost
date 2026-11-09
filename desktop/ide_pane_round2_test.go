//go:build darwin

package main

import "testing"

// Round 2, 7: a reload names the address it is for; one for an address the pane no longer shows
// (the view that asked is gone, another folder is open) does not reload the page.
func TestIDEReloadOnlyWhatIsShown(t *testing.T) {
	s := &ideServer{}
	n := 0
	s.paneReload = func() { n++ }
	s.shown = "http://127.0.0.1:1/?folder=%2FB&tkn=x"
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1:1/?folder=%2FA&tkn=x", false},
		{"", false},
		{"http://127.0.0.1:1/?folder=%2FB&tkn=x", true},
	} {
		if got := s.reload(c.url); got != c.want {
			t.Errorf("reload %q: %v, want %v", c.url, got, c.want)
		}
	}
	if n != 1 {
		t.Fatalf("reloaded %d times", n)
	}
}

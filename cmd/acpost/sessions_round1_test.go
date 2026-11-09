package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// A long session title is cut by characters, not bytes: a Cyrillic one prints as valid UTF-8.
func TestSessionTitlesCutWhole(t *testing.T) {
	title := strings.Repeat("ж", 100)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[{"id":"s1","title":"` + title + `","updated_at":"x","cwd":"/r"}]}`))
	}))
	defer node.Close()
	e := newEnv(t, inbox)
	out := e.ok("", "--node", node.URL, "sessions", "--agent", "fake")
	if !utf8.ValidString(out) || !strings.Contains(out, strings.Repeat("ж", 60)+"…") {
		t.Fatalf("%q", out)
	}
}

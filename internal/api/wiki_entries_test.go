package api

import (
	"net/http"
	"strings"
	"testing"
)

// Every wiki's pages: an empty list and no cursor on a node with none; a limit that is no number
// is invalid; a client token may not read them.
func TestWikiEntriesRoute(t *testing.T) {
	_, tcp, local := testServer(t)
	code, body := call(t, local, "GET", "/v1/wiki/entries", "", "")
	if code != 200 || !strings.Contains(body, `"items":[]`) || !strings.Contains(body, `"next":""`) {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := call(t, local, "GET", "/v1/wiki/entries?limit=x", "", ""); code != http.StatusBadRequest || errCode(body) != "invalid" {
		t.Fatalf("limit: %d %s", code, body)
	}
	if code, body := call(t, tcp, "GET", "/v1/wiki/entries", "Bearer "+clientSecret, ""); code < 400 {
		t.Fatalf("a client token: %d %s", code, body)
	}
}

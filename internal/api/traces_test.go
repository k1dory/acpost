package api

import (
	"net/http"
	"strings"
	"testing"
)

// Traces over HTTP: the node's user reads them; a limit is a number from 1; a client token may not.
func TestTracesRoute(t *testing.T) {
	_, tcp, local := testServer(t)
	if code, body := call(t, local, "POST", "/v1/messages", "", `{"to":"agent:bot","body":"hello"}`); code != http.StatusCreated {
		t.Fatalf("%d %s", code, body)
	}
	code, body := call(t, local, "GET", "/v1/traces?limit=5", "", "")
	if code != 200 || !strings.Contains(body, `"title":"hello"`) || !strings.Contains(body, `"events":[]`) {
		t.Fatalf("%d %s", code, body)
	}
	for _, q := range []string{"?limit=0", "?limit=x"} {
		if code, body := call(t, local, "GET", "/v1/traces"+q, "", ""); code != http.StatusBadRequest || errCode(body) != "invalid" {
			t.Fatalf("%s: %d %s", q, code, body)
		}
	}
	if code, body := call(t, tcp, "GET", "/v1/traces", "Bearer "+clientSecret, ""); code != http.StatusForbidden || errCode(body) != "refused" {
		t.Fatalf("a client: %d %s", code, body)
	}
}

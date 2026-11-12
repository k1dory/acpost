package api

import (
	"encoding/json"
	"testing"
)

// The library is the node's user's: listed over the socket, refused to a token's client, and its
// query parameters checked.
func TestArtifactsRoutes(t *testing.T) {
	_, tcp, local := testServer(t)
	code, body := call(t, local, "GET", "/v1/artifacts?q=x&limit=5", "", "")
	var page struct {
		Items []any  `json:"items"`
		Next  string `json:"next"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &page) != nil || page.Items == nil {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := call(t, tcp, "GET", "/v1/artifacts", "Bearer "+clientSecret, ""); code == 200 || errCode(body) != "refused" {
		t.Errorf("a client listed the library: %d %s", code, body)
	}
	for _, path := range []string{"/v1/artifacts?limit=-1", "/v1/artifacts?before=nope", "/v1/artifacts/head?msg=m&name=a", "/v1/artifacts/head?msg=m&name=a&max=x"} {
		if code, body := call(t, local, "GET", path, "", ""); errCode(body) != "invalid" {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	if code, body := call(t, local, "GET", "/v1/artifacts/head?msg=m&name=a&max=10", "", ""); errCode(body) != "not_found" {
		t.Errorf("a file that is not there: %d %s", code, body)
	}
}

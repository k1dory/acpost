package api

import (
	"strings"
	"testing"
)

// A list with nothing in it is [], never null: a client ranges over it without a nil check.
func TestEmptyListsAreArrays(t *testing.T) {
	_, _, local := testServer(t)
	for _, path := range []string{"/v1/knowledge", "/v1/playbooks", "/v1/schedules", "/v1/backlog", "/v1/mcp", "/v1/wiki"} {
		code, body := call(t, local, "GET", path, "", "")
		if code != 200 || !strings.Contains(body, `"items":[]`) {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	if code, body := call(t, local, "DELETE", "/v1/playbooks/none", "", ""); code != 204 && errCode(body) == "" {
		t.Errorf("delete: %d %s", code, body)
	}
}

package api

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// A node's body with more files than a request may carry is refused at the cap: what is decoded
// does not grow with what the sender pads the list (or the object) with.
func TestLenientDecodeIsBoundedByTheFileCap(t *testing.T) {
	const padded = 200_000
	files := strings.TrimSuffix(strings.Repeat(`{"x":1},`, padded), ",")
	body := []byte(`{"to":"agent:bot","files":[` + files + `]}`)
	var err error
	allocs := testing.AllocsPerRun(1, func() {
		var req client.SendRequest
		err = decodeLenient(json.NewDecoder(bytes.NewReader(body)), &req)
	})
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("%d files: %v", padded, err)
	}
	// A file is a few allocations; at the cap that is thousands, all of the list would be a million.
	if limit := float64(20 * store.MaxFilesPerRequest); allocs > limit {
		t.Fatalf("%.0f allocations to refuse %d files (at most %.0f)", allocs, padded, limit)
	}

	keys := strings.Repeat(`"k":0,`, padded)
	body = []byte(`{` + keys + `"to":"agent:bot","body":"x","files":[{"name":"a.md","data":"eA==","new":1}]}`)
	// Unknown keys around the files are walked past (one buffer, nothing kept); the files keep
	// what a store.File has.
	var req client.SendRequest
	if err := decodeLenient(json.NewDecoder(bytes.NewReader(body)), &req); err != nil || req.Body != "x" || len(req.Files) != 1 || req.Files[0].Name != "a.md" {
		t.Fatalf("padded object: %+v %v", req, err)
	}

	// Files that are not a list, then more keys: the store still sees what was sent there.
	req = client.SendRequest{}
	err = decodeLenient(json.NewDecoder(bytes.NewReader([]byte(`{"files":"nope","zz":"ab"}`))), &req)
	if err == nil || !strings.Contains(err.Error(), "a list") {
		t.Fatalf("files not a list: %v", err)
	}
}

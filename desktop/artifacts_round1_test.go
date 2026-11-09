//go:build darwin

package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// The user attached report.txt and the agent sent back its own report.txt: the reply's is the
// one shown (the node lists the message's files first). An empty file is a file.
func TestMessageFilePrefersTheReply(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/messages/m1/files", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"items": []store.File{
			{Name: "report.txt", Size: 3, Data: []byte("old")},
			{Name: "empty.txt", Size: 0},
			{Name: "report.txt", Size: 3, Reply: true, Data: []byte("new")},
		}})
	})
	n, _ := fakeNode(t, mux)
	a, err := n.MessageFile("m1", "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := base64.StdEncoding.DecodeString(a.Data); string(b) != "new" {
		t.Fatalf("shown %q, want the reply's", b)
	}
	if a, err := n.MessageFile("m1", "empty.txt"); err != nil || a.Size != 0 {
		t.Fatalf("an empty file: %+v %v", a, err)
	}
}

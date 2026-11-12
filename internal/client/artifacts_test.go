package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

func TestArtifactsAsksWhatItIsGiven(t *testing.T) {
	var asked []string
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/v1/artifacts/head" {
			_ = json.NewEncoder(w).Encode(store.File{Name: "a.md", Size: 10, Data: []byte("# a")})
			return
		}
		_ = json.NewEncoder(w).Encode(Artifacts{Items: []store.Artifact{{Kind: "file", Name: "a.md"}}, Next: "1.1.1.0"})
	}))
	defer h.Close()
	c, err := New(h.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	page, err := c.Artifacts(ctx, "a b", "9.0.1.2", 5)
	if err != nil || len(page.Items) != 1 || page.Next != "1.1.1.0" {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := c.Artifacts(ctx, "", "", 0); err != nil {
		t.Fatal(err)
	}
	f, err := c.ArtifactHead(ctx, "msg_1", "a&b.md", 3)
	if err != nil || string(f.Data) != "# a" || f.Size != 10 {
		t.Fatalf("%+v %v", f, err)
	}
	want := []string{"/v1/artifacts?before=9.0.1.2&limit=5&q=a+b", "/v1/artifacts?", "/v1/artifacts/head?max=3&msg=msg_1&name=a%26b.md"}
	for i := range want {
		if i >= len(asked) || asked[i] != want[i] {
			t.Fatalf("asked %q, want %q", asked, want)
		}
	}
}

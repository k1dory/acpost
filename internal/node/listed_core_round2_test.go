package node

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// Files a hub sends are refused unless they are what its listing announced, now that a response
// is read tolerantly: data under a name this version does not read (data_base64) comes empty, and
// is refused here as before, while a field merely added by a newer hub is taken.
func TestFetchedFilesMustBeAsListed(t *testing.T) {
	listed := []store.File{{Name: "a.md", Type: "text/markdown", Size: 1, SHA256: store.Hash([]byte("a"))}}
	for _, tc := range []struct {
		name, items string
		ok          bool
	}{
		{"newer field", `{"name":"a.md","type":"text/markdown","size":1,"data":"YQ==","checksum_v9":"x"}`, true},
		{"data under another name", `{"name":"a.md","type":"text/markdown","size":1,"data_base64":"YQ=="}`, false},
		{"other content", `{"name":"a.md","type":"text/markdown","size":1,"data":"Yg=="}`, false},
		{"other name", `{"name":"b.md","type":"text/markdown","size":1,"data":"YQ=="}`, false},
		{"one more", `{"name":"a.md","size":1,"data":"YQ=="},{"name":"b.md","size":1,"data":"YQ=="}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"items":[`+tc.items+`]}`)
			}))
			defer srv.Close()
			n := newNode(t, hubCfg)
			c, err := client.New(srv.URL, "tok", "")
			if err != nil {
				t.Fatal(err)
			}
			n.hub = c
			got, err := n.fetchFiles(ctx, "msg_x", false, listed, "the message")
			if tc.ok {
				if err != nil || len(got) != 1 || string(got[0].Data) != "a" {
					t.Fatalf("%+v %v", got, err)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) || e.Code != "refused" {
				t.Fatalf("taken: %+v %v", got, err)
			}
		})
	}
}

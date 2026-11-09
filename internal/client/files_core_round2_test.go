package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A file as a newer hub lists it: with a field this version does not know.
const newerFile = `{"name":"a.md","type":"text/markdown","size":1,"data":"YQ==","checksum_v9":"x"}`

const newerMessage = `{"id":"msg_1","from":"user:u@hub","to":"agent:bot@edge","body":"b","state":"done","lease":"L1",` +
	`"files":[` + newerFile + `]}`

// newerHub answers every call a joined node makes with what a newer hub would send.
func newerHub(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/files"):
			fmt.Fprint(w, `{"items":[`+newerFile+`]}`)
		case strings.HasSuffix(r.URL.Path, "/by-idem"), strings.HasSuffix(r.URL.Path, "/disown"):
			fmt.Fprint(w, `{"found":true,"message":`+newerMessage+`}`)
		case r.URL.Path == "/v1/messages":
			fmt.Fprint(w, `{"items":[`+newerMessage+`],"next":"n"}`)
		case r.URL.Query().Get("follow") != "":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: end\ndata: "+newerMessage+"\n\n")
		default:
			fmt.Fprint(w, newerMessage)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Every response that carries files takes a field a newer hub added to them.
func TestResponsesTakeNewerFileFields(t *testing.T) {
	c := newerHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	check := func(what string, files []store.File, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", what, err)
			return
		}
		if len(files) != 1 || files[0].Name != "a.md" || string(files[0].Data) != "a" {
			t.Errorf("%s: files %+v", what, files)
		}
	}
	m, ok, err := c.NodeClaim(ctx, "edge", time.Second, time.Minute)
	if err == nil && !ok {
		err = fmt.Errorf("no message")
	}
	check("NodeClaim", m.Files, err)
	m, err = c.Get(ctx, "msg_1")
	check("Get", m.Files, err)
	m, err = c.Wait(ctx, "msg_1", time.Second)
	check("Wait", m.Files, err)
	files, err := c.SideFiles(ctx, "msg_1", false, 1<<20)
	check("SideFiles", files, err)
	files, err = c.Files(ctx, "msg_1")
	check("Files", files, err)
	m, ok, err = c.ByIdem(ctx, "user:u@edge", "k")
	if err == nil && !ok {
		err = fmt.Errorf("not found")
	}
	check("ByIdem", m.Files, err)
	m, ok, err = c.Disown(ctx, "msg_1", "user:u@edge", "k")
	if err == nil && !ok {
		err = fmt.Errorf("not found")
	}
	check("Disown", m.Files, err)
	page, err := c.List(ctx, ListFilter{})
	if err == nil && (len(page.Items) != 1 || page.Next != "n") {
		err = fmt.Errorf("page %+v", page)
	}
	if err == nil {
		check("List", page.Items[0].Files, nil)
	} else {
		check("List", nil, err)
	}
	m, err = c.Follow(ctx, "msg_1", 0, func(store.Event) error { return nil })
	check("Follow's end", m.Files, err)
	if m.LeaseID != "L1" || m.State != store.Done {
		t.Errorf("the rest of the message was not read: %+v", m)
	}
}

// A user's request is still held to this version's fields, and a response to its bound.
func TestFilesStrictForRequestsBoundedForResponses(t *testing.T) {
	var fs store.Files
	if err := json.Unmarshal([]byte(`[`+newerFile+`]`), &fs); err == nil {
		t.Fatal("a request's misspelt file field was taken")
	}
	many := "[" + strings.Repeat(`{"name":"x"},`, store.MaxFilesPerRequest) + `{"name":"y"}]`
	var rf responseFiles
	if err := json.Unmarshal([]byte(many), &rf); err == nil {
		t.Fatal("a response over the bound on files was taken")
	}
}

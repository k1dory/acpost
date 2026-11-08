package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/node"
)

// An error before the stream sent anything is a plain JSON error with its status; after, it is an
// `event: error` with the same envelope; once done, nothing is written.
func TestSSEStreamErrors(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)

	w := httptest.NewRecorder()
	st, _ := newSSE(w, r)
	st.fail(&node.Error{Code: "not_found", Message: "no terminal x"})
	st.done()
	if w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || errCode(w.Body.String()) != "not_found" {
		t.Fatalf("before the start: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}

	w = httptest.NewRecorder()
	st, _ = newSSE(w, r)
	_ = st.send("event: data\ndata: x\n\n")
	st.fail(&node.Error{Code: "refused", Message: "no longer yours"})
	st.done()
	if err := st.send("event: late\n\n"); err == nil {
		t.Fatal("a write after done")
	}
	out := w.Body.String()
	if w.Code != 200 || !strings.Contains(out, "event: error\ndata: {") || strings.Contains(out, "late") {
		t.Fatalf("after the start: %d %q", w.Code, out)
	}
	data := strings.TrimSpace(out[strings.Index(out, "event: error\ndata: ")+len("event: error\ndata: "):])
	var eb struct {
		Status int        `json:"status"`
		Error  node.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &eb); err != nil || eb.Status != 403 || eb.Error.Code != "refused" {
		t.Fatalf("error event %q: %+v %v", data, eb, err)
	}

	// The client went away: nothing is written for it.
	ctx, cancel := context.WithCancel(context.Background())
	w = httptest.NewRecorder()
	st, _ = newSSE(w, r.WithContext(ctx))
	cancel()
	st.fail(errors.New("context canceled"))
	st.done()
	if w.Body.Len() != 0 {
		t.Fatalf("written for a client that is gone: %q", w.Body.String())
	}
}

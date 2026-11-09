package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A folder made while the request is cut at every moment of its transaction: the answer is made
// (201) or unavailable (503), never final. A cancel between the INSERT and the commit rolls the
// transaction back, and the commit says sql.ErrTxDone, not the cancellation: before, that was
// 400 invalid for a folder that was not kept.
func TestFolderCutDuringItsTransactionIsUnavailable(t *testing.T) {
	n, _, _ := testServer(t)
	h := Handler(n, true, 1<<20)
	seen := map[int]int{}
	for i := range 3000 {
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(time.Duration(i%300)*time.Microsecond, cancel)
		req := httptest.NewRequest("POST", "/v1/folders", strings.NewReader(`{"name":"f"}`)).WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		timer.Stop()
		cancel()
		seen[w.Code]++
		switch w.Code {
		case http.StatusCreated:
			var f struct{ ID string }
			_ = json.Unmarshal(w.Body.Bytes(), &f)
			d := httptest.NewRecorder()
			h.ServeHTTP(d, httptest.NewRequest("DELETE", "/v1/folders/"+f.ID, nil))
		case http.StatusServiceUnavailable:
		default:
			t.Fatalf("a folder cut at %dµs: %d %s (so far %v)", i%300, w.Code, w.Body.String(), seen)
		}
	}
	t.Logf("answers: %v", seen)
}

// Every route, its request cut (the node stops) and its handler failing: the answer is the API's
// unavailable, or the handler's success; never a final error made of the cancellation.
func TestEveryRouteCutIsUnavailable(t *testing.T) {
	n, _, _ := testServer(t)
	h := Handler(n, true, 1<<20)
	param := regexp.MustCompile(`\{[a-z]+\}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failed := 0
	for _, route := range routes(t) {
		method, path, _ := strings.Cut(route, " ")
		path = param.ReplaceAllString(path, "x")
		// A body no handler takes: every handler that reads one fails on it.
		req := httptest.NewRequest(method, path+"?wait=1ms&timeout=1ms", strings.NewReader("not json")).WithContext(ctx)
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { h.ServeHTTP(w, req); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: a cut request did not end", route)
		}
		switch {
		case w.Code == http.StatusServiceUnavailable && errCode(w.Body.String()) == "unavailable":
			failed++
		case w.Code < 300:
		default:
			t.Errorf("%s cut: %d %s", route, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
	if failed < 40 {
		t.Fatalf("only %d routes failed: the table does not reach the handlers' errors", failed)
	}
}

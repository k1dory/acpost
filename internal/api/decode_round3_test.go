package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/client"
)

// files given again and again is refused at the second: what decoding costs stays a few times
// the body, not the body once per repetition.
func TestLenientDecodeRefusesRepeatedFiles(t *testing.T) {
	const reps = 10_000
	body := []byte(`{"to":"agent:bot",` + strings.Repeat(`"files":[],`, reps) + `"body":"x"}`)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var req client.SendRequest
	err := decodeLenient(json.NewDecoder(bytes.NewReader(body)), &req)
	runtime.ReadMemStats(&after)
	if got, limit := after.TotalAlloc-before.TotalAlloc, uint64(20*len(body)); got > limit {
		t.Errorf("%d bytes allocated for a body of %d (at most %d)", got, len(body), limit)
	}
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("%d lists of files: %v", reps, err)
	}
	// One list, as a node sends it, still goes.
	req = client.SendRequest{}
	if err := decodeLenient(json.NewDecoder(strings.NewReader(`{"to":"agent:bot","body":"x","files":[]}`)), &req); err != nil || req.Body != "x" {
		t.Fatalf("one list: %+v %v", req, err)
	}
}

// A write cut short by the stop (its request's context ends) answers unavailable, not invalid:
// nothing was kept, and the client asks again.
func TestWriteCutByTheStopIsUnavailable(t *testing.T) {
	n, _, _ := testServer(t)
	h := Handler(n, true, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/v1/knowledge", `{"name":"n","body":"b","enabled":true}`},
		{"POST", "/v1/folders", `{"name":"f"}`},
		{"GET", "/v1/conversations", ""},
		{"GET", "/v1/usage", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)).WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable || errCode(w.Body.String()) != "unavailable" {
			t.Errorf("%s %s cut short: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// One file padded with a great many fields of no store.File: they are scanned past, not kept
// (no object per field), and the file goes through with what it has.
func TestLenientDecodeSkipsUnknownFieldsOfAFile(t *testing.T) {
	const padded = 200_000
	var pad strings.Builder
	for i := range padded {
		fmt.Fprintf(&pad, `"k%d":0,`, i)
	}
	body := []byte(`{"to":"agent:bot","body":"x","files":[{` + pad.String() + `"name":"a.md","data":"eA=="}]}`)
	var req client.SendRequest
	var err error
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	allocs := testing.AllocsPerRun(1, func() {
		req = client.SendRequest{}
		err = decodeLenient(json.NewDecoder(bytes.NewReader(body)), &req)
	})
	runtime.ReadMemStats(&after)
	if allocs > padded/100 {
		t.Errorf("%.0f allocations for one file with %d unknown fields", allocs, padded)
	}
	// Two runs (AllocsPerRun warms up once): the bytes are a fixed multiple of the body.
	if got, limit := (after.TotalAlloc-before.TotalAlloc)/2, uint64(20*len(body)); got > limit {
		t.Errorf("%d bytes allocated for a body of %d (at most %d)", got, len(body), limit)
	}
	if err != nil || len(req.Files) != 1 || req.Files[0].Name != "a.md" || string(req.Files[0].Data) != "x" {
		t.Fatalf("%+v %v", req.Files, err)
	}
}

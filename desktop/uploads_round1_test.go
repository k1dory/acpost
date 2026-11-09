//go:build darwin

package main

import (
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/k1dory/acpost/internal/client"
)

// Attachments over the node's limits are refused before they are decoded: a 64 MiB file is
// not copied once more to be refused by the node afterwards.
func TestUploadsOverTheLimitAreNotDecoded(t *testing.T) {
	lim := &client.Limits{MaxFiles: 2, MaxFilesSize: 1 << 20}
	big := strings.Repeat("A", 64<<20) // base64 of 48 MiB
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := decodeUploads([]Upload{{Name: "big.bin", Data: big}}, lim)
	runtime.ReadMemStats(&after)
	var e *client.Error
	if !errors.As(err, &e) || e.Code != "invalid" || e.Hint == "" {
		t.Fatalf("want an invalid error with a hint, got %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("%d bytes allocated to refuse it", grew)
	}
	three := []Upload{{Name: "a", Data: "YQ=="}, {Name: "b", Data: "Yg=="}, {Name: "c", Data: "Yw=="}}
	if _, err := decodeUploads(three, lim); !errors.As(err, &e) || e.Code != "invalid" {
		t.Fatalf("3 files over a limit of 2: %v", err)
	}
	fs, err := decodeUploads(three[:2], lim)
	if err != nil || len(fs) != 2 || string(fs[1].Data) != "b" {
		t.Fatalf("%v %v", fs, err)
	}
}

// SendTask checks the limits the node gives (GET /v1/health) and sends nothing over them.
func TestSendTaskChecksTheLimits(t *testing.T) {
	var posts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) { posts.Add(1); w.WriteHeader(201) })
	n, _ := fakeNode(t, mux) // its limits: 4 files, 1 MiB
	_, err := n.SendTask(Task{To: "agent:claude", Body: "hi", Files: []Upload{{Name: "big.bin", Data: strings.Repeat("A", 2<<20)}}})
	if err == nil || !strings.Contains(err.Error(), `"invalid"`) {
		t.Fatalf("want invalid, got %v", err)
	}
	if posts.Load() != 0 {
		t.Fatal("sent to the node anyway")
	}
}

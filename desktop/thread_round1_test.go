//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// threadNode serves a thread of count messages as a node does: GET /v1/messages pages by id
// (at most 200, next the last id when more follow), the list without bodies; GET of one message
// has its body. A message named in fail answers 500.
func threadNode(t *testing.T, count int, fail string) *Node {
	t.Helper()
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i := range count {
		ids = append(ids, fmt.Sprintf("m%04d", i))
	}
	msg := func(id string) store.Message {
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "m"))
		return store.Message{ID: id, Thread: "th", CreatedAt: at.Add(time.Duration(i) * time.Second)}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 200
		}
		after := r.URL.Query().Get("after")
		i := sort.SearchStrings(ids, after)
		if i < len(ids) && ids[i] == after {
			i++
		}
		var p client.Page
		for ; i < len(ids) && len(p.Items) < limit; i++ {
			p.Items = append(p.Items, msg(ids[i]))
		}
		if i < len(ids) {
			p.Next = p.Items[len(p.Items)-1].ID
		}
		json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("GET /v1/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == fail {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":{"code":"internal","message":"disk"}}`)
			return
		}
		m := msg(id)
		m.Body = "body of " + id
		json.NewEncoder(w).Encode(m)
	})
	n, _ := fakeNode(t, mux)
	return n
}

// A thread longer than one page comes whole, the oldest first, every message with its body.
func TestThreadPagesToTheEnd(t *testing.T) {
	n := threadNode(t, 250, "")
	ms, err := n.Thread("th")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 250 {
		t.Fatalf("%d messages, want 250", len(ms))
	}
	for i, m := range ms {
		if want := fmt.Sprintf("m%04d", i); m.ID != want || m.Body != "body of "+want {
			t.Fatalf("message %d: %s %q", i, m.ID, m.Body)
		}
	}
}

// A message that cannot be read fails the thread: not a message without its body, said as fine.
func TestThreadSaysAMessageFailed(t *testing.T) {
	n := threadNode(t, 30, "m0007")
	ms, err := n.Thread("th")
	if err == nil {
		t.Fatalf("no error, %d messages", len(ms))
	}
	if !strings.Contains(err.Error(), "disk") {
		t.Fatalf("the node's error is not passed on: %v", err)
	}
}

//go:build darwin

package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/k1dory/acpost/internal/client"
)

// The composer's key for a cloud task's sandbox reaches the node: the node gives a retry the
// sandbox the first request made (the binding used to drop it, and a lost answer made two).
func TestCreateCloudTaskCarriesTheKey(t *testing.T) {
	var mu sync.Mutex
	var got []client.CloudTask
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/cloud/tasks", func(w http.ResponseWriter, r *http.Request) {
		var ct client.CloudTask
		json.NewDecoder(r.Body).Decode(&ct)
		mu.Lock()
		got = append(got, ct)
		mu.Unlock()
		json.NewEncoder(w).Encode(client.Chat{Key: "cloud:sb_1", Agent: "agent:sb_1"})
	})
	n, _ := fakeNode(t, mux)
	for range 2 {
		if _, err := n.CreateCloudTask("ref1", "github.com/me/a", "do it", "k1"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Idem != "k1" || got[1].Idem != "k1" {
		t.Fatalf("the node got %+v, want both with idem k1", got)
	}
}

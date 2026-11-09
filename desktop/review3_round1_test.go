//go:build darwin

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
)

// Saving over a private file never puts its new content in a file others can read, not even
// the temporary one before the rename.
func TestSaveOverPrivateFileStaysPrivate(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(dest, []byte("old"), 0o600)
	os.Chmod(dest, 0o600)
	var seen os.FileMode
	old := afterWrite
	afterWrite = func(tmp string) {
		if st, err := os.Stat(tmp); err == nil {
			seen = st.Mode().Perm()
		}
	}
	t.Cleanup(func() { afterWrite = old })
	if err := saveFile(dest, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if seen != 0o600 {
		t.Fatalf("the temporary file was %v, want 0600", seen)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o600 {
		t.Fatalf("saved: %v", st.Mode())
	}
}

// A name of the longest length a file may have saves: the temporary name does not grow with it.
func TestSaveLongName(t *testing.T) {
	dest := filepath.Join(t.TempDir(), strings.Repeat("n", 251)+".txt") // 255 bytes
	if err := saveFile(dest, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "x" {
		t.Fatalf("%q", b)
	}
}

// A terminal whose open answers after a few seconds is still closed on the node when the app
// quits meanwhile: shutdown waits for the open as long as the open may take.
func TestShutdownWaitsForASlowOpen(t *testing.T) {
	var posts, closes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/terminals", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		time.Sleep(3500 * time.Millisecond)
		json.NewEncoder(w).Encode(client.Terminal{ID: "t1", Running: true})
	})
	mux.HandleFunc("DELETE /v1/terminals/t1", func(w http.ResponseWriter, r *http.Request) { closes.Add(1); w.WriteHeader(204) })
	n, _ := fakeNode(t, mux)
	go n.OpenTerminal("", 80, 24)
	eventually(t, 2*time.Second, "the open is in flight", func() bool { return posts.Load() == 1 })
	n.ServiceShutdown()
	if closes.Load() != 1 {
		t.Fatalf("%d closes when shutdown returned, want 1", closes.Load())
	}
}

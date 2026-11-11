package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// The stop comes while a request waits on git: the git call is cut, and the answer is the API's
// unavailable (ask again), not a not_found for a repository or a file that is there.
func TestRunStopDuringGitIsUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the git that hangs is a shell script")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	// A git that hangs on the two calls these requests make, and is the real one otherwise.
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in *\"remote get-url\"*|*\"cat-file\"*) exec sleep 30;; esac\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := shortDir(t)
	sock := filepath.Join(dir, "s", "a.sock")
	cfg := testConfig(t, dir, "unix://"+sock)
	repo, tree := t.TempDir(), strings.Repeat("a", 40)
	// The tree is one a turn recorded, so FileAt gets as far as git.
	os.MkdirAll(filepath.Join(dir, "s"), 0o700)
	st, err := store.Open("sqlite://"+filepath.Join(dir, "s", "store.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddChanges(context.Background(), store.TurnChanges{Msg: "m1", Agent: "agent:bot", Repo: repo, Before: tree, After: tree, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	stop := runServer(t, cfg)
	c, _ := client.New("unix://"+sock, "", "")
	res := make(chan error, 2)
	go func() { _, err := c.Forge(context.Background(), client.ForgeQuery{Dir: repo}); res <- err }()
	go func() { _, err := c.FileAt(context.Background(), repo, tree, "f.txt"); res <- err }()
	time.Sleep(500 * time.Millisecond) // both wait on git
	if err, _ := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	for range 2 {
		select {
		case err := <-res:
			if !errors.Is(err, client.ErrUnavailable) {
				t.Fatalf("a request cut by the stop while git ran: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a request did not end with the stop")
		}
	}
}

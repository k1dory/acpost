package node

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

// The link from a wiki to its task is lost after the task was sent (a store write failing, or the
// node stopping there): the wiki still takes the task's pages in, rather than calling the task
// never sent while it runs.
func TestWikiFindsTaskSentButNotLinked(t *testing.T) {
	t.Setenv("FAKE_MODES", "1")
	dir := t.TempDir() + "/s"
	n, _ := fileNode(t, fmt.Sprintf(`store: sqlite://%s/store.db
limits: {max_reply: 256KiB, max_attempts: 2}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, dir, os.Args[0], t.TempDir()))
	db, err := sql.Open("sqlite", "file:"+dir+"/store.db?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The write linking the wiki to its sent task fails.
	if _, err := db.Exec(`CREATE TRIGGER no_link BEFORE UPDATE OF msg ON wikis WHEN NEW.msg NOT LIKE 'pending-%'
		BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	m, _ := n.WriteWiki(ctx, me, t.TempDir(), "agent:fake", "en")
	if m.ID == "" {
		t.Fatal("no task sent")
	}
	if _, err := db.Exec(`DROP TRIGGER no_link`); err != nil {
		t.Fatal(err)
	}
	// As a restart finds it, past the minute a task never sent is given.
	if _, err := db.Exec(`UPDATE wikis SET updated = 0`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		list, err := n.Wikis(ctx, me)
		if err != nil || len(list) != 1 {
			t.Fatalf("%+v %v", list, err)
		}
		if w := list[0]; w.Status != "writing" {
			if w.Status != "ready" || w.Pages != 2 || w.Msg != m.ID {
				t.Fatalf("the wiki of a task that ran: %+v", w)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("still writing")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

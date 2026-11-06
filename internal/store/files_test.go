package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var lim = FileLimits{Count: 16, Size: 1 << 20}

func TestFilesGoWithMessageAndReply(t *testing.T) {
	s := openTest(t, Options{})
	m := msg("agent:a", "see attached")
	m.Files = []File{{Name: "a.md", Type: "text/markdown", Data: []byte("# hi")}, {Name: "b.bin", Type: "application/octet-stream", Data: []byte{0, 1, 2}}}
	got := mustInsert(t, s, m)
	if len(got.Files) != 2 || got.Files[0].Data != nil || got.Files[1].Size != 3 {
		t.Fatalf("insert lists %+v", got.Files)
	}
	g, err := s.Get(ctx, m.ID)
	if err != nil || len(g.Files) != 2 || g.Files[0].Name != "a.md" || g.Files[0].Data != nil || g.Files[0].Size != 4 {
		t.Fatalf("get %+v %v", g.Files, err)
	}
	if list, _, _ := s.List(ctx, Filter{}); len(list[0].Files) != 0 {
		t.Fatal("a list carries files")
	}

	// An attempt's reply files go when the message is claimed again.
	c, _, _ := s.Claim(ctx, "agent:a", time.Minute)
	if err := s.AttachReply(ctx, m.ID, c.LeaseID, []File{{Name: "old.txt", Type: "text/plain", Data: []byte("x")}}, lim); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Release(ctx, m.ID, c.LeaseID, "agent_crashed", "boom", 3); err != nil {
		t.Fatal(err)
	}
	old := c.LeaseID
	c, _, _ = s.Claim(ctx, "agent:a", time.Minute)
	if n, _, _ := s.ReplyFiles(ctx, m.ID); n != 0 {
		t.Fatalf("%d reply files survived a new attempt", n)
	}
	// The earlier attempt may not attach to this one.
	if err := s.AttachReply(ctx, m.ID, old, []File{{Name: "stale.txt", Data: []byte("x")}}, lim); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale attempt: %v", err)
	}
	// Limits count what is attached already, in the same transaction.
	if err := s.AttachReply(ctx, m.ID, c.LeaseID, []File{{Name: "e1"}, {Name: "e2"}, {Name: "e3"}}, FileLimits{Count: 2, Size: 100}); !errors.Is(err, ErrFileLimits) {
		t.Fatalf("over count: %v", err)
	}
	if err := s.AttachReply(ctx, m.ID, c.LeaseID, []File{{Name: "out.txt", Type: "text/plain", Data: []byte("out")}, {Name: "empty.txt"}}, lim); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachReply(ctx, m.ID, c.LeaseID, []File{{Name: "out.txt", Type: "text/plain", Data: []byte("dup")}}, lim); !errors.Is(err, ErrFileExists) {
		t.Fatalf("same name: %v", err)
	}
	// A reply may carry a file of the same name as the message's (the file, edited).
	if err := s.Complete(ctx, m.ID, c.LeaseID, Done, "ok", "", "", File{Name: "a.md", Type: "text/plain", Data: []byte("more")}); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachReply(ctx, m.ID, c.LeaseID, []File{{Name: "late.txt", Data: []byte("late")}}, lim); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("attach after the end: %v", err)
	}
	all, err := s.FilesOf(ctx, m.ID)
	if err != nil || len(all) != 5 {
		t.Fatalf("%+v %v", all, err)
	}
	var names []string
	for _, f := range all {
		names = append(names, fmt.Sprint(f.Name, f.Reply, len(f.Data) == f.Size))
	}
	if strings.Join(names, " ") != "a.mdfalse true b.binfalse true out.txttrue true empty.txttrue true a.mdtrue true" {
		t.Fatalf("files %v", names)
	}
	if !bytes.Equal(all[0].Data, []byte("# hi")) {
		t.Fatalf("data %q", all[0].Data)
	}

	if k, err := s.GC(ctx, time.Now().Add(time.Hour)); err != nil || k != 1 {
		t.Fatalf("gc %d %v", k, err)
	}
	var left int
	s.r.QueryRow("SELECT count(*) FROM files").Scan(&left)
	if left != 0 {
		t.Fatalf("%d files left after gc", left)
	}
}

func TestFilesCountTowardMaxStore(t *testing.T) {
	s := openTest(t, Options{MaxStore: 2 << 20})
	m := msg("agent:a", "big")
	m.Files = []File{{Name: "big", Data: make([]byte, 3<<20)}}
	if _, _, err := s.Insert(ctx, m); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("%v", err)
	}
}

func TestFilesListBoundedWhileDecoding(t *testing.T) {
	var fs Files
	long := "[" + strings.Repeat("{},", MaxFilesPerRequest) + "{}]"
	if err := json.Unmarshal([]byte(long), &fs); err == nil {
		t.Fatal("a list over MaxFilesPerRequest decoded")
	}
	if err := json.Unmarshal([]byte(`[{"name":"a","data":"eA=="}]`), &fs); err != nil || len(fs) != 1 || string(fs[0].Data) != "x" {
		t.Fatalf("%v %+v", err, fs)
	}
}

func TestFilesDecodingStrict(t *testing.T) {
	var fs Files
	if err := json.Unmarshal([]byte(`[{"name":"answer.txt","data_base64":"eA=="}]`), &fs); err == nil {
		t.Fatal("a misspelt field decoded as an empty file")
	}
	both := "[" + strings.Repeat(`{"name":"a"},`, 511) + `{"name":"a"}]`
	if err := json.Unmarshal([]byte(both), &fs); err != nil || len(fs) != 512 {
		t.Fatalf("a listing of both sides at 256 each: %v", err)
	}
}

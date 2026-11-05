package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTest(t testing.TB, opt Options) *Store {
	t.Helper()
	s, err := Open("sqlite://"+filepath.Join(t.TempDir(), "store.db"), opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var ctx = context.Background()

func msg(to, body string) *Message {
	return &Message{ID: NewID(), From: "user:test", To: to, Body: body}
}

func mustInsert(t testing.TB, s *Store, m *Message) Message {
	t.Helper()
	got, _, err := s.Insert(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestInsertGet(t *testing.T) {
	s := openTest(t, Options{})
	m := msg("agent:reviewer", "review PR #123")
	m.Task, m.Meta = "TASK-42", map[string]string{"pr": "123"}
	got := mustInsert(t, s, m)
	if got.State != Queued || got.Thread != got.ID || got.CreatedAt.IsZero() {
		t.Fatalf("inserted: %+v", got)
	}
	back, err := s.Get(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Body != "review PR #123" || back.Task != "TASK-42" || back.Meta["pr"] != "123" || back.To != "agent:reviewer" {
		t.Fatalf("got back %+v", back)
	}
	if _, err := s.Get(ctx, "msg_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestIDsSortByTime(t *testing.T) {
	prev := NewID()
	for range 1000 {
		id := NewID()
		if id <= prev || !strings.HasPrefix(id, "msg_") || len(id) != 30 {
			t.Fatalf("%s after %s", id, prev)
		}
		prev = id
	}
}

func TestIdempotencyKey(t *testing.T) {
	s := openTest(t, Options{})
	first := msg("agent:a", "x")
	first.IdemKey = "k1"
	a, created, err := s.Insert(ctx, first)
	if err != nil || !created {
		t.Fatal(err, created)
	}
	again := msg("agent:a", "x")
	again.IdemKey = "k1"
	b, created, err := s.Insert(ctx, again)
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("repeat: %v %v %s != %s", err, created, b.ID, a.ID)
	}
	other := msg("agent:a", "x")
	other.IdemKey, other.From = "k1", "user:other" // keys are per sender
	if _, created, _ := s.Insert(ctx, other); !created {
		t.Fatal("another sender's key collided")
	}
}

// Every sender racing with the same key gets the same, single message.
func TestIdempotencyKeyConcurrent(t *testing.T) {
	s := openTest(t, Options{})
	var wg sync.WaitGroup
	ids := make(chan string, 64)
	var createdN sync.Map
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := msg("agent:a", "x")
			m.IdemKey = "same"
			got, created, err := s.Insert(ctx, m)
			if err != nil {
				t.Error(err)
				return
			}
			if created {
				createdN.Store(i, true)
			}
			ids <- got.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("two messages for one key: %s %s", first, id)
		}
	}
	n := 0
	createdN.Range(func(_, _ any) bool { n++; return true })
	if n != 1 {
		t.Fatalf("%d inserts reported created", n)
	}
}

func TestClaimCompleteLifecycle(t *testing.T) {
	s := openTest(t, Options{})
	a := mustInsert(t, s, msg("agent:r", "one"))
	b := mustInsert(t, s, msg("agent:r", "two"))
	mustInsert(t, s, msg("agent:other", "elsewhere"))

	c, ok, err := s.Claim(ctx, "agent:r", time.Minute)
	if err != nil || !ok || c.ID != a.ID || c.State != Running || c.Attempts != 1 || c.LeaseID == "" {
		t.Fatalf("first claim %+v %v %v", c, ok, err)
	}
	if err := s.Complete(ctx, c.ID, "wrong-lease", Done, "r", ""); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong lease: %v", err)
	}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "the answer", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, a.ID)
	if got.State != Done || got.Reply != "the answer" || got.LeaseID != "" {
		t.Fatalf("after complete %+v", got)
	}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "again", ""); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("second complete: %v", err)
	}
	c2, ok, _ := s.Claim(ctx, "agent:r", time.Minute)
	if !ok || c2.ID != b.ID {
		t.Fatalf("second claim %+v", c2)
	}
	if _, ok, _ := s.Claim(ctx, "agent:r", time.Minute); ok {
		t.Fatal("claimed from an empty queue")
	}
}

func TestLeaseExpiryRequeuesThenFails(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	for attempt := 1; attempt <= 3; attempt++ {
		c, ok, err := s.Claim(ctx, "agent:r", time.Millisecond)
		if err != nil || !ok || c.Attempts != attempt {
			t.Fatalf("attempt %d: %+v %v %v", attempt, c, ok, err)
		}
		time.Sleep(5 * time.Millisecond)
		if _, err := s.ReapLeases(ctx, time.Now(), 3); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Get(ctx, m.ID)
	if got.State != Failed || !strings.Contains(got.Error, "attempts") {
		t.Fatalf("after 3 lost leases: %+v", got)
	}
}

func TestExtendLease(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	c, _, _ := s.Claim(ctx, "agent:r", 20*time.Millisecond)
	if err := s.Extend(ctx, c.ID, c.LeaseID, time.Minute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if n, _ := s.ReapLeases(ctx, time.Now(), 3); n != 0 {
		t.Fatal("an extended lease was reaped")
	}
	if err := s.Extend(ctx, c.ID, "other", time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
}

func TestExpireAndCancel(t *testing.T) {
	s := openTest(t, Options{})
	m := msg("agent:r", "x")
	m.ExpiresAt = time.Now().Add(-time.Second)
	old := mustInsert(t, s, m)
	if _, ok, _ := s.Claim(ctx, "agent:r", time.Minute); ok {
		t.Fatal("claimed an expired message")
	}
	if n, err := s.Expire(ctx, time.Now()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if got, _ := s.Get(ctx, old.ID); got.State != Expired {
		t.Fatal(got.State)
	}
	c := mustInsert(t, s, msg("agent:r", "y"))
	got, err := s.Cancel(ctx, c.ID)
	if err != nil || got.State != Cancelled {
		t.Fatal(got.State, err)
	}
	if _, err := s.Cancel(ctx, c.ID); !errors.Is(err, ErrFinal) {
		t.Fatalf("cancel twice: %v", err)
	}
}

func TestRequeueRunning(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	mustInsert(t, s, msg("agent:keep", "x"))
	s.Claim(ctx, "agent:r", time.Hour)
	k, _, _ := s.Claim(ctx, "agent:keep", time.Hour)
	if n, err := s.RequeueRunning(ctx, []string{"agent:r"}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, ok, _ := s.Claim(ctx, "agent:r", time.Hour); !ok {
		t.Fatal("not requeued")
	}
	if got, _ := s.Get(ctx, k.ID); got.State != Running {
		t.Fatal("another agent's message was requeued")
	}
}

func TestListPagination(t *testing.T) {
	s := openTest(t, Options{})
	var want []string
	for i := range 1234 {
		m := msg("agent:r", fmt.Sprint(i))
		if i%2 == 0 {
			m.Task = "T"
		}
		want = append(want, mustInsert(t, s, m).ID)
	}
	var got []string
	after := ""
	for {
		page, next, err := s.List(ctx, Filter{To: "agent:r", After: after, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			if m.Body != "" {
				t.Fatal("list returned a body")
			}
			if m.BodySize == 0 {
				t.Fatal("list lost the body size")
			}
			got = append(got, m.ID)
		}
		if next == "" {
			break
		}
		after = next
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pages: %d ids, want %d", len(got), len(want))
	}
	page, _, _ := s.List(ctx, Filter{Task: "T", Limit: 10000})
	if len(page) != MaxPage {
		t.Fatalf("limit not capped: %d", len(page))
	}
}

func TestEventsCapped(t *testing.T) {
	s := openTest(t, Options{MaxEventsPerMessage: 50, MaxEventData: 100})
	m := mustInsert(t, s, msg("agent:r", "x"))
	for i := range 80 {
		if err := s.AddEvent(ctx, m.ID, "tool_call", strings.Repeat("d", 1000)+fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	var all []Event
	after := int64(0)
	for {
		evs, err := s.Events(ctx, m.ID, after, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) == 0 {
			break
		}
		all = append(all, evs...)
		after = evs[len(evs)-1].Seq
	}
	if len(all) != 50 {
		t.Fatalf("%d events kept, want 50", len(all))
	}
	if len(all[0].Data) > 100+len(truncMark) {
		t.Fatalf("event data not truncated: %d", len(all[0].Data))
	}
	if all[49].Kind != "truncated" {
		t.Fatalf("last kept event should say the rest was dropped: %+v", all[49])
	}
}

func TestSessions(t *testing.T) {
	s := openTest(t, Options{})
	if id, _ := s.Session(ctx, "agent:r", "TASK-1"); id != "" {
		t.Fatal(id)
	}
	if err := s.PutSession(ctx, "agent:r", "TASK-1", "sess-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSession(ctx, "agent:r", "TASK-1", "sess-2"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Session(ctx, "agent:r", "TASK-1"); id != "sess-2" {
		t.Fatal(id)
	}
}

func TestMemoryStore(t *testing.T) {
	s, err := Open("memory:", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := mustInsert(t, s, msg("agent:r", "x"))
	if _, err := s.Get(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	// Two memory stores are separate.
	s2, _ := Open("memory:", Options{})
	defer s2.Close()
	if _, err := s2.Get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("memory stores share data")
	}
}

func TestSecondOpenIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open("sqlite://"+path, Options{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("second open: %v", err)
	}
	s.Close()
	s, err = Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	s.Close()
}

func TestFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := Open("sqlite://"+filepath.Join(dir, "store.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode().Perm())
	}
	st, _ = os.Stat(filepath.Join(dir, "store.db"))
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("db mode %v", st.Mode().Perm())
	}
}

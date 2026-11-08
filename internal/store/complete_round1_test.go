package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// A reply whose response was lost is sent again with the same lease: the repeat is recorded
// already, not a conflict. Another lease, or another outcome under the same lease, still is.
func TestCompleteRepeatWithTheSameLease(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	c, ok, err := s.Claim(ctx, "agent:r", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	f := []File{{Name: "a.txt", Data: []byte("hello")}}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "answer", "", "", f...); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "answer", "", "", f...); err != nil {
		t.Fatalf("repeat with the same lease: %v", err)
	}
	if err := s.Complete(ctx, c.ID, "another-lease", Done, "answer", "", ""); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("another lease: %v", err)
	}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Failed, "", "failed", "boom"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("another outcome under the same lease: %v", err)
	}
	if err := s.Complete(ctx, c.ID, "", Done, "answer", "", ""); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("no lease: %v", err)
	}
	got, err := s.Get(ctx, c.ID)
	if err != nil || got.State != Done || got.Reply != "answer" || len(got.Files) != 1 {
		t.Fatalf("after the repeats: %+v %v", got, err)
	}
}

// Ten holders' retries of one reply race: every one is told it is recorded, and it is written once.
func TestCompleteRepeatConcurrent(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	c, _, err := s.Claim(ctx, "agent:r", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.Complete(ctx, c.ID, c.LeaseID, Done, "answer", "", "", File{Name: "r.txt", Data: []byte("r")})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	got, err := s.Get(ctx, c.ID)
	if err != nil || got.State != Done || got.Reply != "answer" || len(got.Files) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

// A store made before the completion column existed gains the column on open.
func TestCompleteRepeatAfterMigration(t *testing.T) {
	path := t.TempDir() + "/s/store.db"
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec("ALTER TABLE messages DROP COLUMN completion"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustInsert(t, s, msg("agent:r", "x"))
	c, _, _ := s.Claim(ctx, "agent:r", time.Minute)
	for range 2 {
		if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "answer", "", ""); err != nil {
			t.Fatal(err)
		}
	}
}

// A repeat is the whole answer again: a file added, or another error text with the same code, is
// another answer under that lease and a conflict, not a success that drops it silently.
func TestCompleteRepeatMustMatch(t *testing.T) {
	s := openTest(t, Options{})
	mustInsert(t, s, msg("agent:r", "x"))
	c, _, _ := s.Claim(ctx, "agent:r", time.Minute)
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "answer", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, c.ID, c.LeaseID, Done, "answer", "", "", File{Name: "report.txt", Data: []byte("r")}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a file added: %v", err)
	}
	mustInsert(t, s, msg("agent:f", "y"))
	f, _, _ := s.Claim(ctx, "agent:f", time.Minute)
	if err := s.Complete(ctx, f.ID, f.LeaseID, Failed, "", "failed", "boom"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, f.ID, f.LeaseID, Failed, "", "failed", "bang"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("another error text: %v", err)
	}
	if err := s.Complete(ctx, f.ID, f.LeaseID, Failed, "", "failed", "boom"); err != nil {
		t.Fatalf("the same failure: %v", err)
	}
	for _, id := range []string{c.ID, f.ID} {
		if got, _ := s.Get(ctx, id); len(got.Files) != 0 || (got.State == Failed && got.Error != "boom") {
			t.Fatalf("%+v", got)
		}
	}
}

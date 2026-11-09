package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Schedule(id) reads its own row: the right one among many, and a row it was not asked for
// (here one whose next_run cannot be read) does not fail the lookup as a table scan did.
func TestScheduleByID(t *testing.T) {
	s := openTest(t, Options{})
	for i := range 50 {
		sc := Schedule{ID: fmt.Sprintf("s%02d", i), Name: fmt.Sprintf("n%02d", 49-i), Cron: "@daily", Agent: "agent:a",
			Body: fmt.Sprintf("body %d", i), Enabled: true, NextRun: time.UnixMilli(int64(1000 + i))}
		if err := s.PutSchedule(ctx, sc); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Schedule(ctx, "s37")
	if err != nil || got.ID != "s37" || got.Name != "n12" || got.Body != "body 37" || got.NextRun.UnixMilli() != 1037 {
		t.Fatalf("s37: %+v %v", got, err)
	}
	if _, err := s.Schedule(ctx, "nope"); !errors.Is(err, ErrNoSchedule) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := s.w.Exec(`UPDATE schedules SET next_run = 'not a time' WHERE id = 's03'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Schedule(ctx, "s37"); err != nil || got.ID != "s37" {
		t.Fatalf("s37 next to an unreadable row: %+v %v", got, err)
	}
}

// Wiki(repo) likewise: its own row and its page count, not the table.
func TestWikiByRepo(t *testing.T) {
	s := openTest(t, Options{})
	for i := range 50 {
		repo := fmt.Sprintf("/r/%02d", i)
		if ok, err := s.StartWiki(ctx, repo, "agent:a", "en", fmt.Sprintf("m%02d", i)); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if err := s.FinishWiki(ctx, "/r/21", "m21", []WikiPage{{Slug: "a", Title: "A"}, {Slug: "b", Title: "B"}}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Wiki(ctx, "/r/21")
	if err != nil || got.Repo != "/r/21" || got.Msg != "m21" || got.Pages != 2 || got.Status == "writing" {
		t.Fatalf("/r/21: %+v %v", got, err)
	}
	if _, err := s.Wiki(ctx, "/r/none"); !errors.Is(err, ErrNoWiki) {
		t.Fatalf("unknown repo: %v", err)
	}
	if _, err := s.w.Exec(`UPDATE wikis SET updated = 'not a time' WHERE repo = '/r/03'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Wiki(ctx, "/r/21"); err != nil || got.Repo != "/r/21" {
		t.Fatalf("/r/21 next to an unreadable row: %+v %v", got, err)
	}
}

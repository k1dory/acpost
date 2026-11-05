package store

// Regression tests from the Codex review (gpt-6-astra, 2026-11-05).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteOutcomeSurvivesLocalMaintenance(t *testing.T) {
	for _, mode := range []string{"ttl", "lease"} {
		t.Run(mode, func(t *testing.T) {
			s := openTest(t, Options{})
			m := msg("agent:r@peer", "work")
			m.Origin = "remote"
			if mode == "ttl" {
				m.ExpiresAt = time.Now().Add(-time.Second)
			}
			got := mustInsert(t, s, m)

			if mode == "ttl" {
				if _, err := s.Expire(ctx, time.Now()); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.Settle(ctx, got.ID, Running, "", "", 3); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ReapLeases(ctx, time.Now(), 3); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Settle(ctx, got.ID, Done, "answer", "", 3); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, got.ID)
			if err != nil || got.State != Done || got.Reply != "answer" {
				t.Fatalf("remote success lost: state=%s reply=%q err=%v",
					got.State, got.Reply, err)
			}
		})
	}
}

func TestUnsettledReadIsPaged(t *testing.T) {
	s := openTest(t, Options{})
	for range MaxPage + 1 {
		m := msg("agent:r@peer", "x")
		m.Origin = "remote"
		mustInsert(t, s, m)
	}
	page, err := s.Unsettled(ctx, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(page) > MaxPage {
		t.Fatalf("recovery materialized %d rows in one read", len(page))
	}
}

func TestExistingStorePathsArePrivateOrRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "store.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		return // Refusing insecure existing paths is also acceptable.
	}
	defer s.Close()

	for _, path := range []string{dir, path} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s remains accessible to other users: %o",
				path, st.Mode().Perm())
		}
	}
}

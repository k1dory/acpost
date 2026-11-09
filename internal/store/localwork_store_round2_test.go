package store

import (
	"testing"
	"time"
)

// copyOf stores a joined node's copy of a message its hub holds, in state, the way a store
// written before copies kept the sender's address has it: under the hub's own unqualified
// address, the same as a local agent's. A running copy carries a lease run out (the state the
// local claim of such a copy left).
func copyOf(t *testing.T, s *Store, origin string, state State) Message {
	t.Helper()
	m := mustInsert(t, s, &Message{ID: NewID(), From: "user:u@edge", To: "agent:bot", Body: "the hub's", Origin: origin})
	if state == Running {
		if _, err := s.w.ExecContext(ctx, `UPDATE messages SET state = 'running', attempts = 5, lease_id = 'old', lease_until = 1 WHERE id = ?`, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// No query over "this node's work for an agent" counts, claims, requeues or reaps a copy whose
// message the hub holds, whatever its recipient reads.
func TestCopiesAreNotLocalWork(t *testing.T) {
	for _, origin := range CopyOrigins {
		t.Run(origin, func(t *testing.T) {
			cases := []struct {
				name  string
				state State
				check func(t *testing.T, s *Store, c Message)
			}{
				{"Claim", Queued, func(t *testing.T, s *Store, c Message) {
					if m, ok, err := s.Claim(ctx, "agent:bot", time.Minute); err != nil || ok {
						t.Fatalf("claimed %s (%v)", m.ID, err)
					}
				}},
				{"ClaimAny", Queued, func(t *testing.T, s *Store, c Message) {
					if m, ok, err := s.ClaimAny(ctx, []string{"agent:other", "agent:bot"}, time.Minute); err != nil || ok {
						t.Fatalf("claimed %s (%v)", m.ID, err)
					}
				}},
				{"InsertLimited", Queued, func(t *testing.T, s *Store, c Message) {
					if _, _, err := s.InsertLimited(ctx, msg("agent:bot", "local"), 1); err != nil {
						t.Fatalf("the copy filled the local queue: %v", err)
					}
				}},
				{"Counts", Queued, func(t *testing.T, s *Store, c Message) {
					if q, r, err := s.Counts(ctx, "agent:bot"); err != nil || q != 0 || r != 0 {
						t.Fatalf("counted %d queued %d running (%v)", q, r, err)
					}
				}},
				{"Counts running", Running, func(t *testing.T, s *Store, c Message) {
					if q, r, err := s.Counts(ctx, "agent:bot"); err != nil || q != 0 || r != 0 {
						t.Fatalf("counted %d queued %d running (%v)", q, r, err)
					}
				}},
				{"RequeueRunning", Running, func(t *testing.T, s *Store, c Message) {
					if k, err := s.RequeueRunning(ctx, []string{"agent:bot"}, 3); err != nil || k != 0 {
						t.Fatalf("requeued %d (%v)", k, err)
					}
				}},
				{"ReapLeases", Running, func(t *testing.T, s *Store, c Message) {
					if k, err := s.ReapLeases(ctx, time.Now(), 3); err != nil || k != 0 {
						t.Fatalf("reaped %d (%v)", k, err)
					}
				}},
				{"ActiveRecipients", Queued, func(t *testing.T, s *Store, c Message) {
					if got, err := s.ActiveRecipients(ctx, "agent:"); err != nil || len(got) != 0 {
						t.Fatalf("active %v (%v)", got, err)
					}
				}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					s := openTest(t, Options{})
					c := copyOf(t, s, origin, tc.state)
					tc.check(t, s, c)
					after, err := s.Get(ctx, c.ID)
					if err != nil {
						t.Fatal(err)
					}
					if after.State != c.State || after.Attempts != c.Attempts || after.LeaseID != c.LeaseID {
						t.Fatalf("the copy changed: %s/%d/%q, was %s/%d/%q", after.State, after.Attempts, after.LeaseID, c.State, c.Attempts, c.LeaseID)
					}
				})
			}
		})
	}
}

// The same queries still see local work and the deliveries a hub hands this node (origin hub:…).
func TestLocalWorkStillCounted(t *testing.T) {
	for _, origin := range []string{"", "hub:L1"} {
		s := openTest(t, Options{})
		m := mustInsert(t, s, &Message{ID: NewID(), From: "user:u", To: "agent:bot", Body: "mine", Origin: origin})
		if q, _, _ := s.Counts(ctx, "agent:bot"); q != 1 {
			t.Fatalf("%q: counted %d", origin, q)
		}
		if _, _, err := s.InsertLimited(ctx, msg("agent:bot", "more"), 1); err != ErrQueueFull {
			t.Fatalf("%q: a full queue took more: %v", origin, err)
		}
		if got, ok, err := s.Claim(ctx, "agent:bot", time.Minute); err != nil || !ok || got.ID != m.ID {
			t.Fatalf("%q: claim %v %v", origin, ok, err)
		}
		if k, err := s.RequeueRunning(ctx, []string{"agent:bot"}, 3); err != nil || k != 1 {
			t.Fatalf("%q: requeued %d (%v)", origin, k, err)
		}
	}
}

// A copy finished from the hub's message takes its qualified address, and keeps the address it
// was sent to when the hub's is unqualified (the node could not qualify it). A copy replaced by the
// hub's message for a key takes the hub's address as given: the node qualifies it, and the
// repeat's own address may name another agent.
func TestCopyKeepsQualifiedAddress(t *testing.T) {
	for _, tc := range []struct{ path, sentTo, hubTo, want string }{
		{"FinishForward", "agent:bot@hub", "agent:bot", "agent:bot@hub"},
		{"FinishForward", "agent:bot", "agent:bot@other", "agent:bot@other"},
		{"FinishForward", "agent:bot@hub", "agent:bot@hub", "agent:bot@hub"},
		{"ReplaceForward", "agent:b@hub", "agent:a@hub", "agent:a@hub"},
		{"ReplaceForward", "agent:b@hub", "agent:a", "agent:a"},
	} {
		s := openTest(t, Options{})
		m := mustInsert(t, s, &Message{ID: NewID(), From: "user:u@edge", To: tc.sentTo, Body: "b", Origin: "forwarding"})
		hub := Message{ID: m.ID, To: tc.hubTo, Body: "b"}
		if tc.path == "ReplaceForward" {
			hub.ID = NewID()
			if _, err := s.ReplaceForward(ctx, m.ID, "forwarding", "remote", hub); err != nil {
				t.Fatal(err)
			}
		} else if ok, err := s.FinishForward(ctx, m.ID, "forwarding", "remote", hub); err != nil || !ok {
			t.Fatal(ok, err)
		}
		got, err := s.Get(ctx, hub.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.To != tc.want {
			t.Errorf("%s: sent to %s, hub says %s: the copy reads %s, want %s", tc.path, tc.sentTo, tc.hubTo, got.To, tc.want)
		}
	}
}

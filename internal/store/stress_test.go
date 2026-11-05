package store

// The store's three promises, each tested under load:
//   - concurrent writers, readers and claimers never fail and never lose or double-deliver a message;
//   - the file does not grow without bound: retention and vacuum give the space back, churn
//     reaches a steady size, and max_store refuses sends instead of filling the disk;
//   - reading everything that is stored does not need memory in proportion to it.
//
// ACPOST_HEAVY=1 multiplies the volumes (gigabyte scale) for a manual run.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func heavy() bool { return os.Getenv("ACPOST_HEAVY") == "1" }

func scale(n, heavyN int) int {
	if heavy() {
		return heavyN
	}
	if testing.Short() {
		return max(n/10, 1)
	}
	return n
}

// TestConcurrentWritersReadersClaimers: 32 writers, 8 readers and 8 claimers at once. No call may
// fail (no SQLITE_BUSY reaches a caller), every message is inserted once, and every message is
// claimed and completed exactly once.
func TestConcurrentWritersReadersClaimers(t *testing.T) {
	s := openTest(t, Options{})
	const writers, readers, claimers = 32, 8, 8
	perWriter := scale(300, 3000)
	total := writers * perWriter

	var errs atomic.Int64
	fail := func(err error) {
		if errs.Add(1) <= 5 {
			t.Error(err)
		}
	}
	var claimed sync.Map // id -> claimer
	var completed atomic.Int64
	writersDone := make(chan struct{})

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				m := msg(fmt.Sprintf("agent:a%d", i%4), strings.Repeat("b", 512))
				m.Task = fmt.Sprintf("T%d", w)
				m.IdemKey = fmt.Sprintf("w%d-%d", w, i)
				if _, _, err := s.Insert(ctx, m); err != nil {
					fail(err)
				}
			}
		}()
	}
	var rg sync.WaitGroup
	for range readers {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for {
				select {
				case <-writersDone:
					return
				default:
				}
				page, _, err := s.List(ctx, Filter{Limit: 200})
				if err != nil {
					fail(err)
					continue
				}
				for _, m := range page[:min(len(page), 5)] {
					if _, err := s.Get(ctx, m.ID); err != nil {
						fail(err)
					}
				}
			}
		}()
	}
	var cg sync.WaitGroup
	for c := range claimers {
		cg.Add(1)
		go func() {
			defer cg.Done()
			idle := 0
			for completed.Load() < int64(total) && idle < 2000 {
				m, ok, err := s.Claim(ctx, fmt.Sprintf("agent:a%d", c%4), time.Hour)
				if err != nil {
					fail(err)
					continue
				}
				if !ok {
					idle++
					time.Sleep(time.Millisecond)
					continue
				}
				idle = 0
				if prev, dup := claimed.LoadOrStore(m.ID, c); dup {
					fail(fmt.Errorf("%s claimed by %v and %d", m.ID, prev, c))
				}
				if err := s.Complete(ctx, m.ID, m.LeaseID, Done, "ok", ""); err != nil {
					fail(err)
				}
				completed.Add(1)
			}
		}()
	}
	wg.Wait()
	close(writersDone)
	rg.Wait()
	cg.Wait()

	if errs.Load() > 0 {
		t.Fatalf("%d calls failed", errs.Load())
	}
	if completed.Load() != int64(total) {
		t.Fatalf("completed %d of %d", completed.Load(), total)
	}
	n := 0
	after := ""
	for {
		page, next, err := s.List(ctx, Filter{State: Done, After: after, Limit: MaxPage})
		if err != nil {
			t.Fatal(err)
		}
		n += len(page)
		if next == "" {
			break
		}
		after = next
	}
	if n != total {
		t.Fatalf("%d done messages stored, want %d", n, total)
	}
	if err := s.IntegrityCheck(ctx); err != nil {
		t.Fatal(err)
	}
}

// Pages stay consistent while writers append: every id that existed when the walk began is seen
// exactly once.
func TestListWhileWriting(t *testing.T) {
	s := openTest(t, Options{})
	before := map[string]bool{}
	for range 2000 {
		before[mustInsert(t, s, msg("agent:r", "x")).ID] = false
	}
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				s.Insert(ctx, msg("agent:r", "new"))
			}
		}
	}()
	after := ""
	for {
		page, next, err := s.List(ctx, Filter{To: "agent:r", After: after, Limit: 97})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			if seen, ok := before[m.ID]; ok {
				if seen {
					t.Fatalf("%s seen twice", m.ID)
				}
				before[m.ID] = true
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	close(stop)
	for id, seen := range before {
		if !seen {
			t.Fatalf("%s missed", id)
		}
	}
}

func fileSize(t testing.TB, s *Store) int64 {
	t.Helper()
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if st, err := os.Stat(s.Path() + suffix); err == nil {
			total += st.Size()
		}
	}
	return total
}

const MiB = 1 << 20

// fillDone stores n final messages with bodies and replies of size bytes each.
func fillDone(t testing.TB, s *Store, n, size int) {
	t.Helper()
	body := strings.Repeat("x", size)
	for i := range n {
		m := msg(fmt.Sprintf("agent:a%d", i%8), body)
		mustInsert(t, s, m)
	}
	for a := range 8 {
		for {
			c, ok, err := s.Claim(ctx, fmt.Sprintf("agent:a%d", a), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			if err := s.Complete(ctx, c.ID, c.LeaseID, Done, body, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Retention gives the space back to the file system, not only to SQLite's free list.
func TestGrowthRetentionReclaimsSpace(t *testing.T) {
	s := openTest(t, Options{})
	n := scale(2000, 20000)
	fillDone(t, s, n, 32<<10) // body + reply: 64 KiB a message
	peak := fileSize(t, s)
	if peak < int64(n)*64<<10/2 {
		t.Fatalf("store is only %d bytes after writing %d messages: test is not measuring anything", peak, n)
	}
	deleted, err := s.GC(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != n {
		t.Fatalf("gc deleted %d of %d", deleted, n)
	}
	if err := s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	after := fileSize(t, s)
	t.Logf("peak %d MiB, after gc %d KiB", peak/MiB, after>>10)
	if after > 4*MiB {
		t.Fatalf("after deleting everything the store still takes %d bytes (peak %d)", after, peak)
	}
}

// Steady traffic with retention reaches a steady size: the file does not creep up round after round.
func TestGrowthSteadyUnderChurn(t *testing.T) {
	s := openTest(t, Options{})
	rounds := scale(20, 60)
	var sizes []int64
	var peaks []int64
	for range rounds {
		roundStart := time.Now()
		fillDone(t, s, 300, 16<<10)
		peaks = append(peaks, fileSize(t, s))
		// Retention keeps the last round, deletes the ones before it.
		if _, err := s.GC(ctx, roundStart); err != nil {
			t.Fatal(err)
		}
		if err := s.Maintain(ctx); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, fileSize(t, s))
	}
	t.Logf("peak size per round (KiB): %v", kib(peaks))
	sizes = append(sizes, peaks...)
	t.Logf("sizes per round (KiB): %v", kib(sizes))
	settled := max(slicesMax(sizes[:3]), slicesMax(peaks[:3]))
	for i, sz := range sizes[3:] {
		if sz > settled+settled/5+MiB {
			t.Fatalf("round %d: %d bytes, first rounds peaked at %d", i+3, sz, settled)
		}
	}
}

// The WAL does not grow past its limit while writes keep coming and readers hold snapshots.
func TestGrowthWALBounded(t *testing.T) {
	s := openTest(t, Options{JournalSizeLimit: 8 * MiB})
	stop := make(chan struct{})
	var rg sync.WaitGroup
	for range 4 {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s.List(ctx, Filter{Limit: 50})
				}
			}
		}()
	}
	body := strings.Repeat("w", 64<<10)
	var maxWAL int64
	// No Maintain here: SQLite's own checkpoints (wal_autocheckpoint) and journal_size_limit
	// must keep the WAL in bounds between the node's periodic maintenance.
	for i := range scale(3000, 30000) {
		mustInsert(t, s, msg("agent:r", body))
		if i%20 == 0 {
			if st, err := os.Stat(s.Path() + "-wal"); err == nil {
				maxWAL = max(maxWAL, st.Size())
			}
		}
	}
	close(stop)
	rg.Wait()
	t.Logf("largest WAL seen: %d MiB", maxWAL/MiB)
	if maxWAL > 32*MiB {
		t.Fatalf("WAL grew to %d bytes", maxWAL)
	}
}

// Over max_store a send is refused; the store does not keep growing.
func TestGrowthMaxStoreRefuses(t *testing.T) {
	s := openTest(t, Options{MaxStore: 16 * MiB})
	body := strings.Repeat("z", 256<<10)
	refused := false
	for range 1000 {
		_, _, err := s.Insert(ctx, msg("agent:r", body))
		if errors.Is(err, ErrStoreFull) {
			refused = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !refused {
		t.Fatal("never refused")
	}
	s.Maintain(ctx)
	if sz := fileSize(t, s); sz > 24*MiB {
		t.Fatalf("store at %d bytes with max 16 MiB", sz)
	}
	// Space freed by retention makes room again.
	if _, err := s.Cancel(ctx, mustFirst(t, s)); err != nil {
		t.Fatal(err)
	}
	s.GC(ctx, time.Now().Add(time.Hour))
	s.Maintain(ctx)
	if _, _, err := s.Insert(ctx, msg("agent:r", body)); err != nil {
		t.Fatalf("still refused after gc: %v", err)
	}
}

func mustFirst(t testing.TB, s *Store) string {
	page, _, err := s.List(ctx, Filter{Limit: 1})
	if err != nil || len(page) == 0 {
		t.Fatal(err)
	}
	return page[0].ID
}

// heapSampler records the peak live heap while it runs.
type heapSampler struct {
	stop chan struct{}
	done chan struct{}
	peak atomic.Uint64
}

func sampleHeap() *heapSampler {
	h := &heapSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > h.peak.Load() {
				h.peak.Store(ms.HeapAlloc)
			}
			select {
			case <-h.stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	return h
}

func (h *heapSampler) Stop() uint64 { close(h.stop); <-h.done; return h.peak.Load() }

// Reading every message (pages of the list, then each message in full) needs about the memory of
// one page and one message, however much is stored.
func TestMemoryReadingEverything(t *testing.T) {
	s := openTest(t, Options{})
	n := scale(3000, 20000)
	fillDone(t, s, n, 32<<10) // 64 KiB a message: 190 MiB by default, 1.3 GiB heavy
	stored := fileSize(t, s)

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	h := sampleHeap()
	seen := 0
	after := ""
	for {
		page, next, err := s.List(ctx, Filter{After: after, Limit: MaxPage})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			full, err := s.Get(ctx, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(full.Body) != 32<<10 {
				t.Fatal("short body")
			}
			seen++
		}
		if next == "" {
			break
		}
		after = next
	}
	peak := h.Stop()
	grew := int64(peak) - int64(base)
	t.Logf("stored %d MiB in %d messages; heap grew by at most %d MiB while reading all", stored/MiB, n, grew/MiB)
	if seen != n {
		t.Fatalf("read %d of %d", seen, n)
	}
	if grew > 48*MiB {
		t.Fatalf("heap grew by %d bytes reading %d bytes", grew, stored)
	}
}

// A node killed mid-write leaves a store that opens and passes an integrity check.
func TestCrashMidWriteLeavesConsistentStore(t *testing.T) {
	if os.Getenv("ACPOST_CRASH_CHILD") != "" {
		s, err := Open("sqlite://"+os.Getenv("ACPOST_CRASH_CHILD"), Options{})
		if err != nil {
			os.Exit(3)
		}
		body := strings.Repeat("c", 8<<10)
		for i := 0; ; i++ {
			m := &Message{ID: NewID(), From: "user:crash", To: "agent:r", Body: body}
			s.Insert(ctx, m)
			if i%50 == 0 {
				if c, ok, _ := s.Claim(ctx, "agent:r", time.Hour); ok {
					s.Complete(ctx, c.ID, c.LeaseID, Done, body, "")
				}
			}
		}
	}
	path := filepath.Join(t.TempDir(), "store.db")
	for round := range 3 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashMidWriteLeavesConsistentStore$")
		cmd.Env = append(os.Environ(), "ACPOST_CRASH_CHILD="+path)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(300+round*200) * time.Millisecond)
		cmd.Process.Signal(syscall.SIGKILL)
		cmd.Wait()
		s, err := Open("sqlite://"+path, Options{})
		if err != nil {
			t.Fatalf("round %d: reopen: %v", round, err)
		}
		if err := s.IntegrityCheck(ctx); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		page, _, err := s.List(ctx, Filter{Limit: 1})
		if err != nil || len(page) == 0 {
			t.Fatalf("round %d: nothing written before the kill (%v)", round, err)
		}
		s.Close()
	}
}

func kib(v []int64) []int64 {
	out := make([]int64, len(v))
	for i, x := range v {
		out[i] = x >> 10
	}
	return out
}

func slicesMax(v []int64) int64 {
	m := v[0]
	for _, x := range v {
		m = max(m, x)
	}
	return m
}

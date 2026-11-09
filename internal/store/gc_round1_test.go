package store

import (
	"strings"
	"testing"
)

// Collecting orphan content searches files by the files_hash index instead of scanning them per
// blob, and still keeps what a file refers to.
func TestOrphanBlobsUseTheHashIndex(t *testing.T) {
	s := openTest(t, Options{})
	rows, err := s.w.QueryContext(ctx, "EXPLAIN QUERY PLAN "+orphanBlobs)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var id, parent, aux int
		var detail string
		if err := rows.Scan(&id, &parent, &aux, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	rows.Close()
	if p := strings.Join(plan, "; "); !strings.Contains(p, "files_hash") || strings.Contains(p, "SCAN f") {
		t.Fatalf("plan: %s", p)
	}

	kept := mustInsert(t, s, withFiles(msg("agent:a", "x"), File{Name: "k.txt", Data: []byte("kept")}))
	gone := mustInsert(t, s, withFiles(msg("agent:a", "y"), File{Name: "g.txt", Data: []byte("gone")}))
	if _, err := s.w.ExecContext(ctx, `DELETE FROM files WHERE msg = (SELECT seq FROM messages WHERE id = ?)`, gone.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dropOrphans(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM blobs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("blobs left: %d %v", n, err)
	}
	got, err := s.Get(ctx, kept.ID)
	if err != nil || len(got.Files) != 1 {
		t.Fatalf("kept: %+v %v", got, err)
	}
}

func withFiles(m *Message, f ...File) *Message {
	m.Files = f
	return m
}

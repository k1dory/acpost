//go:build darwin

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func readRecord(t *testing.T, data string) ideRecord {
	t.Helper()
	var rec ideRecord
	b, err := os.ReadFile(filepath.Join(data, idePidFile))
	if err == nil {
		err = json.Unmarshal(b, &rec)
	}
	if err != nil {
		t.Fatalf("pid file: %v (%q)", err, b)
	}
	return rec
}

// Round 2, 3: the server is recorded (pid, boot, start time) whole, or the start fails and its
// group is ended: a server no record names could not be found again after a crash.
func TestIDERecordWrittenOrStartFails(t *testing.T) {
	t.Run("written", func(t *testing.T) {
		f := newFakeIDE(t, "serve")
		if _, err := f.open(""); err != nil {
			t.Fatal(err)
		}
		rec := readRecord(t, f.data)
		run := f.current()
		if _, start, ok := ideProc(run.cmd.Process.Pid); rec.Pid != run.cmd.Process.Pid || rec.Boot != ideBoot() || !ok || rec.Start != start {
			t.Fatalf("record %+v, server %d started %q", rec, run.cmd.Process.Pid, start)
		}
		if m, _ := filepath.Glob(filepath.Join(f.data, ".serve-web.pid-*")); len(m) != 0 {
			t.Fatalf("temporary files left: %v", m)
		}
	})
	t.Run("not writable", func(t *testing.T) {
		f := newFakeIDE(t, "serve")
		// The pid file's name taken by a folder: the record cannot be put there.
		if err := os.Mkdir(filepath.Join(f.data, idePidFile), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := f.open("")
		if err == nil || !strings.Contains(err.Error(), "not recorded") {
			t.Fatalf("a start without its record: %v", err)
		}
		run := f.current()
		if run == nil || !waitClosed(run.done, 3*time.Second) {
			t.Fatal("the unrecorded server still runs")
		}
		if m, _ := filepath.Glob(filepath.Join(f.data, ".serve-web.pid-*")); len(m) != 0 {
			t.Fatalf("temporary files left: %v", m)
		}
	})
}

func writeRecord(t *testing.T, data string, rec ideRecord) {
	t.Helper()
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(data, idePidFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Round 2, 1: nothing is signalled on a record that does not prove the group is this app's server:
// a number taken again by a group whose leader has gone (its processes not VS Code's of this
// folder), a live server whose start time is not the recorded one, a record of another boot.
func TestIDEOrphanOwnershipProven(t *testing.T) {
	t.Run("leaderless foreign group", func(t *testing.T) {
		f := newFakeIDE(t, "serve")
		// A group of this test's: its leader ends at once, a process of it stays.
		sh := exec.Command("/bin/sh", "-c", "sleep 30 & exit 0")
		sh.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := sh.Start(); err != nil {
			t.Fatal(err)
		}
		pg := sh.Process.Pid
		t.Cleanup(func() { syscall.Kill(-pg, syscall.SIGKILL) })
		sh.Wait()
		if groupGone(pg, 0) {
			t.Fatal("the group under test is gone")
		}
		writeRecord(t, f.data, ideRecord{Pid: pg, Boot: ideBoot(), Start: "Mon Jan  1 00:00:00 2024"})
		if _, err := f.open(""); err != nil {
			t.Fatal(err)
		}
		if groupGone(pg, 300*time.Millisecond) {
			t.Fatal("a group not this app's was ended as its orphan")
		}
	})
	for _, c := range []struct {
		name string
		edit func(*ideRecord)
	}{
		{"another start time", func(r *ideRecord) { r.Start = "Mon Jan  1 00:00:00 2024" }},
		{"another boot", func(r *ideRecord) { r.Boot = "00000000-0000-0000-0000-000000000000" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeIDE(t, "serve")
			if _, err := f.open(""); err != nil {
				t.Fatal(err)
			}
			other := f.current()
			f.disown()
			rec := readRecord(t, f.data)
			c.edit(&rec)
			writeRecord(t, f.data, rec)
			g := &ideServer{findCLI: f.findCLI, dataDir: f.dataDir, install: f.install, port: f.port, budget: f.budget}
			t.Cleanup(g.stop)
			if _, err := g.open(""); err != nil {
				t.Fatal(err)
			}
			if waitClosed(other.done, 300*time.Millisecond) {
				t.Fatal("a server the record does not prove was ended")
			}
		})
	}
	// The proven cases still end it: covered by TestIDEOrphanEndedSamePort (leader there) and
	// TestIDEStuckGroupRefusesStart (leaderless).
}

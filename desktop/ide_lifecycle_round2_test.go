//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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

// Round 2, 6: the IDE's port free when looked at and taken before the server listens (another
// program starting at the same moment): the start goes on, on a port the server picks.
func TestIDEPortTakenAsItStarts(t *testing.T) {
	f := newFakeIDE(t, "serve")
	var thief net.Listener
	f.ideServer.starts = func(port int) {
		if port == f.port && thief == nil {
			thief, _ = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		}
	}
	t.Cleanup(func() {
		if thief != nil {
			thief.Close()
		}
	})
	base, err := f.open("")
	if err != nil {
		t.Fatalf("a start whose port was taken as it started: %v", err)
	}
	if thief == nil {
		t.Fatal("the port was not taken: nothing under test")
	}
	if p := portOf(t, base); p == f.port || p == 0 {
		t.Fatalf("port %d", p)
	}
	if n := f.starts(); n != 2 {
		t.Fatalf("the CLI ran %d times", n)
	}
}

// Round 2, 4: a server that ends while the app's look is installed (it served, then its CLI
// ended) is not given as started: the install is cut short and the start fails.
func TestIDEServerEndsDuringInstall(t *testing.T) {
	f := newFakeIDE(t, "serve")
	cut := make(chan bool, 1)
	f.install = func(ctx context.Context, data string) error {
		http.Get("http://127.0.0.1:" + strconv.Itoa(f.port) + "/exit")
		select {
		case <-ctx.Done():
			cut <- true
			return ctx.Err()
		case <-time.After(5 * time.Second):
			cut <- false
			return nil
		}
	}
	_, err := f.open("")
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("a start whose server ended during the install: %v", err)
	}
	if !<-cut {
		t.Error("the install was not cut short by the server's end")
	}
	if run := f.current(); run == nil || !closed(run.done) {
		t.Fatal("its group is not ended")
	}
}

// Round 2, 5: stop lets the IDE folder go only once the installers have ended: a repeat install
// still running (its server already gone) is cut short and waited for first.
func TestIDEStopWaitsForInstall(t *testing.T) {
	f := newFakeIDE(t, "serve")
	var calls atomic.Int32
	var ended atomic.Bool
	started := make(chan struct{})
	f.install = func(ctx context.Context, data string) error {
		if calls.Add(1) == 1 {
			return errors.New("first install failed")
		}
		close(started)
		<-ctx.Done()
		time.Sleep(400 * time.Millisecond) // VS Code's installer ending after its cancel
		ended.Store(true)
		return ctx.Err()
	}
	if _, err := f.open(""); err != nil {
		t.Fatal(err)
	}
	f.open("") // the repeat install, beside the server
	if !waitClosed(started, 3*time.Second) {
		t.Fatal("the repeat install did not start")
	}
	run := f.current()
	http.Get("http://127.0.0.1:" + strconv.Itoa(f.port) + "/exit")
	if !waitClosed(run.done, 4*time.Second) {
		t.Fatal("the server did not end")
	}
	f.stop()
	if !ended.Load() {
		t.Fatal("stop let the folder go while an installer was still in it")
	}
	f.mu.Lock()
	owner := f.owner
	f.mu.Unlock()
	if owner != nil {
		t.Fatal("the lock is still held after stop")
	}
}

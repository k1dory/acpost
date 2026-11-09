//go:build darwin

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

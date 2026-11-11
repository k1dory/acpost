package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/plat"
)

// A configuration file someone named must be there, by --config or ACPOST_CONFIG, also when the
// name is the default's own path: exit 2, no node on defaults. Only the default no one named may
// be absent: the node then starts on defaults (autostarted without a --config it would refuse).
func TestNamedConfigMustExist(t *testing.T) {
	e := newEnv(t, inbox)
	xdg, state := filepath.Join(e.dir, "xdg"), filepath.Join(e.dir, "st")
	implicit := filepath.Join(xdg, "acpost", "config.yaml")
	base := []string{"PATH=" + binDir + ":" + os.Getenv("PATH"), "HOME=" + e.dir, "XDG_CONFIG_HOME=" + xdg, "XDG_STATE_HOME=" + state}
	pidFile := filepath.Join(state, "acpost", "acpost.pid")
	t.Cleanup(func() { // the node started on defaults, if any: only one of ours (its binary, serve)
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 1 {
			return
		}
		out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		if err != nil || !strings.Contains(string(out), filepath.Join(binDir, "acpost")+" serve") {
			return
		}
		askToStop(pid)
		until(10*time.Second, func() bool { return !plat.Alive(pid) })
	})
	for _, c := range []struct {
		name string
		vars []string
		args []string
	}{
		{"--config absent", nil, []string{"--config", filepath.Join(e.dir, "absent.yaml"), "status"}},
		{"--config the default's path", nil, []string{"--config", implicit, "status"}},
		{"ACPOST_CONFIG the default's path", []string{"ACPOST_CONFIG=" + implicit}, []string{"status"}},
	} {
		e.vars = append(append([]string{}, base...), c.vars...)
		if r := e.run("", c.args...); r.code != exitUsage || !strings.Contains(r.err, "no such file") {
			t.Errorf("%s: exit %d: %s", c.name, r.code, r.err)
		}
		if _, err := os.Stat(pidFile); err == nil {
			t.Fatalf("%s: a node was started", c.name)
		}
	}
	e.vars = base
	if r := e.run("", "status"); r.code != 0 {
		t.Fatalf("the implicit default, absent: exit %d: %s", r.code, r.err)
	}
}

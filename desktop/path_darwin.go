//go:build darwin

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// fixPath gives the app the PATH a terminal has. Launched from Finder or the Dock, an app gets
// launchd's (/usr/bin:/bin:/usr/sbin:/sbin): agents that are node scripts (claude-agent-acp,
// codex-acp) then exit 127 — no node. The login shell's PATH is asked (in 3 s), and the usual
// places are added after it whatever it says.
func fixPath() {
	seen := map[string]bool{}
	var dirs []string
	add := func(list string) {
		for _, d := range filepath.SplitList(list) {
			if d != "" && !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	add(loginPath(shell))
	add(os.Getenv("PATH"))
	home, _ := os.UserHomeDir()
	add(strings.Join([]string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", filepath.Join(home, ".local/bin"),
		filepath.Join(home, ".bun/bin"), filepath.Join(home, ".npm-global/bin"), filepath.Join(home, ".volta/bin"),
		filepath.Join(home, "go/bin"), filepath.Join(home, ".cargo/bin"), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, string(filepath.ListSeparator)))
	os.Setenv("PATH", strings.Join(dirs, string(filepath.ListSeparator)))
}

// loginPath is the PATH the user's login shell sets up (-l: the profile, Homebrew's shellenv,
// nvm…; -i: zsh's .zshrc), or "" in 3 s. The shell runs in a process group of its own, killed
// whole at the deadline (a profile that starts something holding the pipe does not hold the
// app), its output read up to 64 KiB; a marker keeps the PATH apart from what the profile prints.
func loginPath(shell string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.Command(shell, "-l", "-i", "-c", `printf '\n__ACPOST_PATH__%s\n' "$PATH"`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = nil
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return ""
	}
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(out, 64<<10))
		done <- b
	}()
	var b []byte
	select {
	case b = <-done:
	case <-ctx.Done():
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // the shell and whatever it started
	_ = cmd.Wait()
	for _, line := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(line, "__ACPOST_PATH__"); ok {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

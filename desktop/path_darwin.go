//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// -l: the login profile (Homebrew's shellenv, nvm…); -i for zsh's .zshrc; the marker keeps the
	// PATH apart from whatever the profile prints.
	if out, err := exec.CommandContext(ctx, shell, "-l", "-i", "-c", `printf '\n__ACPOST_PATH__%s\n' "$PATH"`).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if p, ok := strings.CutPrefix(line, "__ACPOST_PATH__"); ok {
				add(p)
			}
		}
	}
	add(os.Getenv("PATH"))
	home, _ := os.UserHomeDir()
	add(strings.Join([]string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", filepath.Join(home, ".local/bin"),
		filepath.Join(home, ".bun/bin"), filepath.Join(home, ".npm-global/bin"), filepath.Join(home, ".volta/bin"),
		filepath.Join(home, "go/bin"), filepath.Join(home, ".cargo/bin"), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, string(filepath.ListSeparator)))
	os.Setenv("PATH", strings.Join(dirs, string(filepath.ListSeparator)))
}

package node

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/plat"
)

// CLI is the acpost command of the program that runs the node: `acpost serve` names itself, the
// app names the command it carries (the app's own binary is a window, not a command). Set before
// a node starts; "" leaves the agents' PATH as it is.
//
// An agent is told to run `acpost send`, `acpost attach` (the preamble), and its PATH is the
// node's: where acpost was started by its full path, or is the app's, no directory of PATH has
// it. So the node puts its own command first on the PATH of what it starts — the one that speaks
// this node's API, whatever other acpost a directory of PATH holds.
var CLI string

var cliDir struct {
	once sync.Once
	dir  string
}

// cliOnPath is PATH with the directory of the node's own command first, or PATH as it was.
func cliOnPath(path string) string {
	cliDir.once.Do(func() { cliDir.dir = linkCLI(CLI, filepath.Join(config.StateDir(), "bin")) })
	if cliDir.dir == "" {
		return path
	}
	if path == "" {
		return cliDir.dir
	}
	return cliDir.dir + string(os.PathListSeparator) + path
}

// linkCLI makes dir hold the command alone, under the name agents call it by, and returns dir
// ("" when there is no command or the link cannot be made). A directory of its own: the one the
// command lives in (~/go/bin, Homebrew's) put first would reorder every other program in it.
// On Windows, where a link needs a privilege, the command's directory is used when the command is
// named acpost.exe there.
func linkCLI(cli, dir string) string {
	if cli == "" {
		return ""
	}
	if st, err := os.Stat(cli); err != nil || st.IsDir() {
		return ""
	}
	if runtime.GOOS == "windows" {
		if strings.EqualFold(filepath.Base(cli), "acpost.exe") {
			return filepath.Dir(cli)
		}
		return ""
	}
	link := filepath.Join(dir, "acpost")
	if have, err := os.Readlink(link); err == nil && have == cli {
		return dir
	}
	if err := plat.MkdirPrivate(dir); err != nil {
		return ""
	}
	// Made beside and renamed over: an agent running the old link meanwhile finds one or the other.
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(cli, tmp); err != nil {
		return ""
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return ""
	}
	return dir
}

// withCLI is env with the node's command first on its PATH (the last PATH of env is the one a
// process gets: the agent's own `env: {PATH: …}` keeps its directories, after the command's).
func withCLI(env []string) []string {
	if CLI == "" {
		return env
	}
	at := -1
	for i, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k == "PATH" || (runtime.GOOS == "windows" && strings.EqualFold(k, "PATH")) {
			at = i
		}
	}
	if at < 0 {
		if p := cliOnPath(""); p != "" {
			return append(env, "PATH="+p)
		}
		return env
	}
	k, v, _ := strings.Cut(env[at], "=")
	out := append([]string(nil), env...)
	out[at] = k + "=" + cliOnPath(v)
	return out
}

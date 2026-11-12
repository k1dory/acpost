package node

import (
	"path/filepath"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

// learnProduct keeps what the agent said it is when its process initialized: its programmatic
// name, else its title. An agent that says nothing leaves the last known value.
func (a *acpAgent) learnProduct(init acp.InitializeResponse) {
	if init.AgentInfo == nil {
		return
	}
	v := strings.TrimSpace(init.AgentInfo.Name)
	if v == "" && init.AgentInfo.Title != nil {
		v = strings.TrimSpace(*init.AgentInfo.Title)
	}
	if v != "" {
		a.product.Store(&v)
	}
}

// productName is the program behind the agent: what it said when it last started, else the name of
// the program its command runs. No process is started to learn it.
func (a *acpAgent) productName() string {
	if v := a.product.Load(); v != nil {
		return *v
	}
	return commandProduct(a.cfg.Command)
}

// launchers run another program named after them (npx some-agent, uvx …): the program is that one.
var launchers = map[string]bool{
	"npx": true, "bunx": true, "pnpx": true, "uvx": true, "pipx": true, "node": true, "bun": true, "deno": true,
	"python": true, "python3": true, "uv": true, "env": true, "exec": true, "pnpm": true, "yarn": true,
	"npm": true, "run": true, "dlx": true, "tool": true, "x": true,
}

// commandProduct names the program a command runs: its first word that is not a launcher or a flag,
// as a base name without a Windows extension or a package's version (npx -y @scope/codex-acp@1 →
// codex-acp). "" for an empty command.
func commandProduct(cmd []string) string {
	for _, w := range cmd {
		if w == "" || strings.HasPrefix(w, "-") {
			continue
		}
		name := w
		if at := strings.LastIndex(name, "@"); at > 0 {
			name = name[:at] // @scope/pkg@1.2 → @scope/pkg
		}
		name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
		for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1", ".js", ".mjs", ".py"} {
			if strings.HasSuffix(strings.ToLower(name), ext) {
				name = name[:len(name)-len(ext)]
				break
			}
		}
		if name == "" || name == "." || name == "/" {
			continue
		}
		if launchers[strings.ToLower(name)] {
			continue
		}
		return name
	}
	return ""
}

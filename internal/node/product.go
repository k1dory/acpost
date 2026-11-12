package node

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
)

// maxProduct bounds what an agent says it is: a program's name, not a text.
const maxProduct = 64

// learnProduct keeps what the agent said it is when its process initialized: its programmatic
// name, else its title. It is the agent's own text: kept only printable and short. An agent that
// says nothing usable leaves the last known value.
func (a *acpAgent) learnProduct(init acp.InitializeResponse) {
	if init.AgentInfo == nil {
		return
	}
	v := printable(init.AgentInfo.Name)
	if v == "" && init.AgentInfo.Title != nil {
		v = printable(*init.AgentInfo.Title)
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

// printable is s trimmed, when it is short and all printable; else "".
func printable(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxProduct {
		return ""
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return ""
		}
	}
	return s
}

// commandProduct names the agent program a command runs (npx -y @scope/codex-acp@1 → codex-acp),
// as every client that lists the agents sees it. A command holds what the configuration put there
// (an env assignment, an option's value can be a key): the word found as the program is only a
// candidate, and what is returned is the name of a known agent program it matches, from the table
// below, never the command's own text. "" for anything else.
func commandProduct(cmd []string) string {
	return knownPrograms[strings.ToLower(baseName(programOf(cmd)))]
}

// knownPrograms are the commands and packages (npm, pip) of the coding agents the app draws, and
// their ACP adapters, to the name the node says: the only text a command's product can be.
var knownPrograms = func() map[string]string {
	m := map[string]string{}
	for _, n := range []string{
		"claude", "claude-code", "claude-agent-acp", "claude-code-acp",
		"codex", "codex-acp",
		"gemini", "gemini-cli",
		"copilot", "copilot-cli", "copilot-acp",
		"opencode", "opencode-ai",
		"cursor-agent", "cursor-acp",
		"amp", "amp-acp", "ampcode",
		"devin", "devin-acp",
		"kiro", "kiro-cli",
		"goose", "goose-cli",
		"qwen", "qwen-code",
		"kimi", "kimi-cli",
		"deepseek", "deepseek-cli",
		"cline", "cline-cli",
		"aider", "aider-chat", "aider-acp",
		"zed",
		"auggie", "augment",
		"codebuddy", "codebuddy-code",
	} {
		m[n] = n
	}
	return m
}()

// The options of the launchers the program is found behind that take the next word.
var (
	envOperand  = map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true}
	pkgOperand  = map[string]bool{"-p": true, "--package": true, "--from": true, "--with": true, "-w": true, "--python": true, "--registry": true, "--cache": true, "--userconfig": true, "--prefix": true, "--index-url": true, "--spec": true, "--pip-args": true, "--node-arg": true}
	nodeOperand = map[string]bool{"-r": true, "--require": true, "--import": true, "--loader": true, "--experimental-loader": true, "--conditions": true, "-C": true, "--title": true, "--env-file": true, "-W": true, "-X": true}
	shells      = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "cmd": true, "powershell": true, "pwsh": true}
)

// programOf is the word of cmd that names the program it runs ("" when not known): past env and
// its assignments, a package runner and its options, an interpreter to its script or module.
func programOf(cmd []string) string {
	for len(cmd) > 0 {
		name, args := strings.ToLower(baseName(cmd[0])), cmd[1:]
		switch name {
		case "env":
			cmd = skipOptions(args, envOperand, true)
		case "npx", "pnpx", "bunx", "uvx":
			return firstWord(skipOptions(args, pkgOperand, false))
		case "pipx", "pnpm", "yarn", "npm", "bun", "uv":
			args = skipOptions(args, pkgOperand, false)
			if len(args) > 1 && args[0] == "tool" && args[1] == "run" { // uv tool run
				args = args[1:]
			}
			if len(args) == 0 {
				return ""
			}
			switch {
			case name == "bun" && args[0] == "run":
				return script(args[1:], false) // bun run agent.ts
			case args[0] == "x" || args[0] == "dlx" || args[0] == "exec" || args[0] == "run":
				return firstWord(skipOptions(args[1:], pkgOperand, false))
			case name == "bun":
				return script(args, false) // bun agent.ts
			}
			return ""
		case "node", "nodejs", "deno", "tsx", "ts-node", "python", "python3", "py":
			return script(args, name == "deno")
		default:
			if shells[name] {
				return "" // a shell runs whatever its script says
			}
			return cmd[0]
		}
	}
	return ""
}

// script is the script or module an interpreter runs (python -m mod, node path/to/agent.js, with
// sub, deno run x.ts); "" for code given inline (-e, -c) or none.
func script(args []string, sub bool) string {
	for i := 0; i < len(args); i++ {
		w := args[i]
		switch {
		case w == "-m":
			return firstWord(args[i+1:])
		case w == "-e" || w == "--eval" || w == "-p" || w == "--print" || w == "-c":
			return ""
		case nodeOperand[w]:
			i++
		case strings.HasPrefix(w, "-"):
		case sub && w == "run":
			sub = false
		default:
			return w
		}
	}
	return ""
}

// skipOptions drops the options before the program (with an option's operand), and with assign
// the NAME=value assignments env takes; "--" ends them.
func skipOptions(args []string, operand map[string]bool, assign bool) []string {
	for i := 0; i < len(args); i++ {
		w := args[i]
		switch {
		case w == "--":
			return args[i+1:]
		case strings.HasPrefix(w, "-"): // env -: an empty environment, as -i
			if operand[w] {
				i++
			}
		case assign && strings.Contains(w, "="):
		default:
			return args[i:]
		}
	}
	return nil
}

func firstWord(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// baseName is a word's program or package name: the last element of its path (either separator),
// without a package's version (@scope/pkg@1.2 → pkg) or a Windows or script extension.
func baseName(w string) string {
	if at := strings.LastIndex(w, "@"); at > 0 {
		w = w[:at]
	}
	w = filepath.Base(strings.ReplaceAll(w, `\`, "/"))
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1", ".js", ".mjs", ".cjs", ".ts", ".py"} {
		if len(w) > len(ext) && strings.EqualFold(w[len(w)-len(ext):], ext) {
			return w[:len(w)-len(ext)]
		}
	}
	return w
}

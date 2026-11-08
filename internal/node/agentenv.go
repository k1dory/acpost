package node

// What an agent process (and what it starts) gets of the node's environment: everything the
// README promises (PATH, HOME, the agent's own API keys, proxies), never the node's secrets. A
// `permissions: allow` agent runs `env` as easily as anything else, and a hub's join token or
// sandboxd's API token in its hands is the node's authority, not the one ACPOST_TOKEN scopes.

import (
	"strings"

	"github.com/k1dory/acpost/internal/config"
)

// nodeSecretPrefixes are variables of acpost and of the services a node talks to with its own
// credentials (sandboxd, as sandboxctl reads it; a hub), named or not in the configuration.
var nodeSecretPrefixes = []string{"ACPOST_", "SANDBOXD_", "HUB_"}

// agentEnv is the node's environment without its secrets: the ACPOST_, SANDBOXD_ and HUB_
// variables, every variable a token_env of the configuration names, and any variable whose value
// is one of the node's tokens (whatever it is called). The agent's own env goes on top, as given.
func agentEnv(cfg *config.Config, environ []string, own map[string]string) []string {
	named := secretEnvNames(cfg)
	secrets := secretValues(cfg)
	var env []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if named[k] || hasAnyPrefix(k, nodeSecretPrefixes) || isSecret(cfg, secrets, v) {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range own {
		env = append(env, k+"="+v)
	}
	return env
}

// secretEnvNames are the variables the configuration reads tokens from.
func secretEnvNames(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	add := func(name string) {
		if name != "" {
			out[name] = true
		}
	}
	if cfg == nil {
		return out
	}
	if j := cfg.Node.Join; j != nil {
		add(j.TokenEnv)
	}
	for _, t := range cfg.Tokens {
		if t != nil {
			add(t.TokenEnv)
		}
	}
	if c := cfg.Cloud; c != nil {
		add(c.TokenEnv)
	}
	for _, a := range cfg.Agents {
		if a != nil && a.Sandbox != nil {
			add(a.Sandbox.TokenEnv)
		}
	}
	return out
}

// secretValues are the tokens the node holds in the clear.
func secretValues(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	add := func(v string) {
		if len(v) >= 8 { // a token, not a word that happens to match
			out[v] = true
		}
	}
	if cfg == nil {
		return out
	}
	if j := cfg.Node.Join; j != nil {
		add(j.Token)
	}
	if c := cfg.Cloud; c != nil {
		add(c.Token)
	}
	for _, a := range cfg.Agents {
		if a != nil && a.Sandbox != nil {
			add(a.Sandbox.Token)
		}
	}
	return out
}

// isSecret says whether a value is one of the node's tokens: held in the clear, or a client
// token the configuration knows by its hash.
func isSecret(cfg *config.Config, clear map[string]bool, v string) bool {
	if len(v) < 8 {
		return false
	}
	if clear[v] {
		return true
	}
	if cfg != nil {
		for _, t := range cfg.Tokens {
			if t != nil && t.Matches(v) {
				return true
			}
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

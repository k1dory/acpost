package node

import (
	"regexp"
	"strings"
)

// What a tool call did is kept and relayed (to the hub, to the sender); secrets in it are not:
// fields named like one are masked, and so are the usual ways of passing one on a command line.

const masked = "[redacted]"

var secretName = regexp.MustCompile(`(?i)(pass(word|wd)?|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|authorization|auth|cookie|credential|session[_-]?key)`)

// secretText finds a secret written in text: a bearer token or basic credentials, a NAME=value,
// name: value or "name": value whose name is a secret's (JSON too; an array value to its closing
// bracket across lines, one level nested, else to the end of the line), a
// --token-like flag's value, credentials in a URL.
var secretText = regexp.MustCompile(`(?i)` +
	`((?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]{8,}` +
	`|((?:"[^"\n]{0,64}?(?:password|passwd|secret|token|api_?key|access_?key|private_?key|credentials?|authorization)[^"\n]{0,64}?"|[A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIALS?))\s*[=:]\s*)("(?:[^"\\]|\\.)*"|'[^']*'|\[(?:[^\[\]]|\[[^\[\]]*\])*\]|\[[^\n]*|[^\s"',}]+)` +
	`|(--?(?:password|passwd|token|secret|api-?key|access-?key)(?:\s+|=))("[^"]*"|'[^']*'|[^\s"']+)` +
	`|(://[^/\s:@]+:)[^@\s/]+(@)`)

// redact masks the secrets text carries.
func redact(s string) string {
	if s == "" {
		return s
	}
	return secretText.ReplaceAllStringFunc(s, func(m string) string {
		sub := secretText.FindStringSubmatch(m)
		switch {
		case sub[1] != "":
			return sub[1] + masked
		case sub[2] != "":
			return sub[2] + masked
		case sub[4] != "":
			return sub[4] + masked
		case sub[6] != "":
			return sub[6] + masked + sub[7]
		}
		return masked
	})
}

// maskFields is a copy of v with the values of fields named like a secret masked (env maps,
// headers, options; a value that is not an object is masked whole), and the secrets written in
// its strings masked, at any depth.
func maskFields(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if _, obj := x.(map[string]any); !obj && secretName.MatchString(k) && !strings.EqualFold(k, "author") {
				out[k] = masked
				continue
			}
			out[k] = maskFields(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = maskFields(x)
		}
		return out
	case string:
		return redact(t)
	}
	return v
}

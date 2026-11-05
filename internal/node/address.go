package node

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/k1dory/acpost/internal/config"
)

// Address is kind:name or kind:name@node.
type Address struct {
	Kind string // agent, user, client, node
	Name string
	Node string // "" for this node (or, for an agent, wherever it is)
}

func (a Address) String() string {
	s := a.Kind + ":" + a.Name
	if a.Node != "" {
		s += "@" + a.Node
	}
	return s
}

var kinds = map[string]bool{"agent": true, "user": true, "client": true, "node": true}

// ParseAddress reads an address. A bare name is an agent's.
func ParseAddress(s string) (Address, error) {
	s = strings.TrimSpace(s)
	kind, rest, ok := strings.Cut(s, ":")
	if !ok {
		kind, rest = "agent", s
	}
	name, node, hasNode := strings.Cut(rest, "@")
	a := Address{Kind: kind, Name: name, Node: node}
	switch {
	case !kinds[kind]:
		return a, fmt.Errorf("address %q: kind is agent, user, client or node", s)
	case !config.ValidName(name):
		return a, fmt.Errorf("address %q: the name may have letters, digits, . _ -", s)
	case hasNode && !config.ValidName(node):
		return a, fmt.Errorf("address %q: bad node name", s)
	}
	return a, nil
}

// matchAny says whether addr matches one of the glob patterns ("*" matches anything; "agent:*",
// "user:*@laptop"…).
func matchAny(patterns []string, addr string) bool {
	for _, p := range patterns {
		if p == "*" {
			return true
		}
		if ok, _ := path.Match(p, addr); ok {
			return true
		}
	}
	return false
}

// Error is a failure with a code the API and the command line map to a status and an exit code.
type Error struct {
	Code    string `json:"code"` // invalid, not_found, refused, conflict, timeout, unavailable, unsupported
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"` // what to do next
}

// errh is errf with a hint.
func errh(code, hint, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Hint: hint}
}

// hintOf is the hint of err ("" for none).
func hintOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

func (e *Error) Error() string { return e.Message }

func errf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf is the code of err ("internal" when it has none).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "internal"
}

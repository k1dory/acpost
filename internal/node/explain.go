package node

// Failures an agent (or a person) can act on: a stable code, one sentence, and what to do next.
// The agent's own JSON-RPC error is turned into these here, in one place.

import (
	"errors"
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/k1dory/acpost/internal/store"
)

// Failure is why a message failed.
type Failure struct {
	Code    string
	Message string
}

// hints says what to do about each failure code (README, "Errors").
var hints = map[string]string{
	"session_busy":      "close the conversation in the other client (editor, app, CLI), or send without --session to start a new one",
	"session_not_found": "`acpost sessions --agent <name>` lists the agent's sessions; or send without --session",
	"agent_auth":        "log the agent in on the node's machine (codex login, claude /login), or set auth_method and its API key in the node's config",
	"agent_start":       "check the agent's command in the node's config (is it installed and on PATH?); `acpost serve -v` logs its stderr",
	"agent_crashed":     "the agent process exited mid-turn and was retried up to limits.max_attempts; `acpost serve -v` logs its stderr",
	"turn_timeout":      "raise the agent's turn_timeout, or split the task into smaller messages",
	"agent_stopped":     "the agent ended the turn itself; rephrase or split the task",
	"unsupported":       "the agent does not offer this; use another agent or leave the option out",
	"lease_lost":        "the consumer of the message stopped answering; check it, then send again",
	"node_restart":      "the node restarted during the last allowed attempt; send again",
	"expired":           "no agent took the message before its --ttl; send again with a longer --ttl, or check the agent is served",
	"agent_error":       "the agent reported an error (in `error`); check the agent and its configuration",
	"failed":            "the consumer of the message reported it failed (in `error`)",
}

// HintFor is what to do about a failure code ("" for none).
func HintFor(code string) string { return hints[code] }

// withHint fills a message's hint from its error code.
func withHint(m store.Message) store.Message {
	if m.ErrorCode != "" && m.Hint == "" {
		m.Hint = HintFor(m.ErrorCode)
	}
	return m
}

func (f Failure) Error() string { return f.Message }

// failf makes a Failure.
func failf(code, format string, args ...any) Failure {
	return Failure{Code: code, Message: fmt.Sprintf(format, args...)}
}

// explainACP turns an error from an ACP call into a Failure. op says what was being done ("load
// session X"); session says the error concerns a session the sender named.
func explainACP(err error, op string, session bool) Failure {
	var f Failure
	if errors.As(err, &f) {
		return f
	}
	var re *acp.RequestError
	if !errors.As(err, &re) {
		return failf("agent_error", "%s: %v", op, err)
	}
	reason := ""
	if data, ok := re.Data.(map[string]any); ok {
		reason, _ = data["reason"].(string)
	}
	text := strings.ToLower(re.Message)
	switch {
	case reason == "thread_active_writer" || strings.Contains(text, "in use by another") || strings.Contains(text, "active writer"):
		return failf("session_busy", "%s: the session is open in another client of the agent", op)
	case re.Code == acp.NewAuthRequired(nil).Code || strings.Contains(text, "auth"):
		return failf("agent_auth", "%s: the agent needs to be logged in", op)
	case re.Code == acp.NewMethodNotFound("").Code:
		return failf("unsupported", "%s: the agent does not offer it", op)
	case session:
		return failf("session_not_found", "%s: the agent has no such session (%s)", op, agentWords(re.Message))
	}
	return failf("agent_error", "%s: %s", op, agentWords(re.Message))
}

// agentWords is the agent's message without the JSON-RPC prefix, short.
func agentWords(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "Invalid request:"))
	s = strings.TrimPrefix(s, "Invalid params:")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return strings.TrimSpace(s)
}

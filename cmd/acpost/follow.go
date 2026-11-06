package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// followMessage shows a message as it runs: the agent's text on stdout as it writes it, what it
// does (tools, plan, permissions, notices) on stderr, then the outcome as `wait` has it. The reply
// is what counts: when the text streamed is not it (reply: last drops text before a tool call; an
// attempt was retried; the stream was cut), the reply follows under "--- reply". With --json: one
// line per event, then the final message.
func followMessage(ctx context.Context, g *globals, c *client.Client, id string) error {
	var streamed strings.Builder
	open := false // stdout's last line has no end yet
	note := func(format string, args ...any) {
		if open {
			fmt.Fprintln(os.Stderr)
			open = false
		}
		fmt.Fprintf(os.Stderr, "· "+format+"\n", args...)
	}
	m, err := c.Follow(ctx, id, 0, func(e store.Event) error {
		if g.json {
			printJSON(e)
			return nil
		}
		var d map[string]any
		_ = json.Unmarshal([]byte(e.Data), &d)
		str := func(k string) string { s, _ := d[k].(string); return s }
		switch e.Kind {
		case "text":
			fmt.Print(str("text"))
			streamed.WriteString(str("text"))
			open = !strings.HasSuffix(str("text"), "\n")
		case "thought": // the agent's thinking: not part of its answer
		case "tool_call":
			note("%s", strings.TrimSpace(str("title")+" "+bracket(str("kind"))))
		case "tool_call_update":
			if str("status") == "failed" {
				note("failed: %s", str("title"))
			}
		case "permission":
			note("permission %s: %s", str("decision"), str("tool"))
		case "plan":
			var steps []string
			_ = json.Unmarshal([]byte(e.Data), &steps)
			note("plan: %s", strings.Join(steps, "; "))
		default: // notice, truncated, and kinds to come
			var s string
			if json.Unmarshal([]byte(e.Data), &s) != nil {
				s = e.Data
			}
			note("%s: %s", e.Kind, s)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if g.json {
		printJSON(m)
	} else {
		switch got := streamed.String(); {
		case got == "": // an inbox consumer's answer, or one from another node: it comes whole
			fmt.Print(m.Reply)
			open = m.Reply != "" && !strings.HasSuffix(m.Reply, "\n")
		case got != m.Reply:
			if open {
				fmt.Println()
			}
			if m.Reply == "" {
				fmt.Print("\n--- reply (empty)\n")
			} else {
				fmt.Print("\n--- reply\n" + m.Reply)
			}
			open = m.Reply != "" && !strings.HasSuffix(m.Reply, "\n")
		}
		if open {
			fmt.Println()
		}
		noteFiles(m.ID, m.Files, true)
	}
	if m.State != store.Done {
		code := m.ErrorCode
		if code == "" {
			code = string(m.State)
		}
		why := m.Error
		if why == "" {
			why = string(m.State)
		}
		return &exitErr{code: exitFailed, name: code, hint: m.Hint, err: errors.New(m.ID + " " + string(m.State) + ": " + why)}
	}
	return nil
}

func bracket(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}

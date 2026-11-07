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
	open := false   // stdout's last line has no end yet
	broken := false // a note began its own line after it: stdout still ends it, at the end
	var used []store.Usage
	note := func(format string, args ...any) {
		if open && !broken {
			fmt.Fprintln(os.Stderr)
			broken = true
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
			open, broken = !strings.HasSuffix(str("text"), "\n"), false
		case "thought": // the agent's thinking: not part of its answer
		case "usage": // said once, at the end
			var u store.Usage
			if json.Unmarshal([]byte(e.Data), &u) == nil {
				used = append(used, u)
			}
		case "tool_call":
			note("%s", strings.TrimSpace(str("title")+" "+bracket(str("kind"))))
		case "tool_call_update":
			if str("status") == "failed" {
				note("failed: %s", str("title"))
			}
		case "permission":
			note("permission %s: %s", str("decision"), str("tool"))
		case "permission_request":
			note("asks permission: %s — acpost approve %s %s (or --reject)", str("title"), id, str("request_id"))
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
		if s := usageLine(used); s != "" {
			fmt.Fprintln(os.Stderr, "· "+s)
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

// usageLine says in one line what the turns used: tokens, the cache's share, the cost.
func usageLine(used []store.Usage) string {
	var tok, read, in, cw int64
	var cost float64
	cur, costed := "", false
	for _, u := range used {
		tok += int64(u.Input + u.Output + u.CacheRead + u.CacheWrite)
		read, in, cw = read+int64(u.CacheRead), in+int64(u.Input), cw+int64(u.CacheWrite)
		if u.Cost != nil {
			cost, cur, costed = cost+*u.Cost, u.Currency, true
		}
	}
	if tok == 0 && !costed {
		return ""
	}
	s := tokens(tok) + " tokens"
	if all := in + read + cw; all > 0 {
		s += fmt.Sprintf(", %.0f%% from the cache", float64(read)*100/float64(all))
	}
	if costed {
		s += fmt.Sprintf(", %.4f %s", cost, cur)
	}
	return s
}

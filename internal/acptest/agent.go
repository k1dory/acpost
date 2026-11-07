// Package acptest is a scripted ACP agent for tests. Run serves ACP on stdin/stdout. What it does is driven by the prompt's last line:
//
//	echo TEXT        answers TEXT
//	tools            says "thinking", makes a tool call, then answers "final"
//	permission       asks permission for a tool call; answers "allowed" or "denied"
//	sleep MS         takes MS milliseconds (or until cancelled)
//	stream N MS      thinks "hmm", then answers "w1 w2 … wN" a word at a time, MS milliseconds apart
//	costly COST MS   says the session's cumulative COST in a usage_update, then takes MS milliseconds
//	usage IN OUT CR CW [COST]  answers "used": the turn's tokens in its response, the session's
//	                 cumulative COST (USD) in a usage_update when given
//	crash            exits mid-turn
//	count            answers how many prompts its session has had
//	env NAME         answers the value of $NAME
//	big N            answers N bytes
//	refuse           ends the turn with stopReason refusal
//	prompt           answers the whole prompt it got
//	sessions         answers how many sessions this process created
//	shell CMD        runs CMD with sh -c and answers its combined output
//	give [N]         answers "here" (N bytes of text instead) with files in its answer: an image, a link to a URL, the
//	                 file $FAKE_DISK embedded (it exists with that content), new embedded text,
//	                 a resource_link to the file $FAKE_LINK, and each resource it was given
//	                 handed back as a resource_link
//	blocks           answers one line per non-text block it got (FAKE_PLAIN=1: it declares no
//	                 embedded context or images)
package acptest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type fakeAgent struct {
	conn     *acp.AgentSideConnection
	mu       sync.Mutex
	prompts  map[acp.SessionId]int
	cancels  map[acp.SessionId]context.CancelFunc
	sessions atomic.Int64
}

// Run serves the scripted agent on stdin/stdout until the client goes, then exits.
func Run() {
	a := &fakeAgent{prompts: map[acp.SessionId]int{}, cancels: map[acp.SessionId]context.CancelFunc{}}
	a.conn = acp.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	<-a.conn.Done()
	os.Exit(0)
}

func (a *fakeAgent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{LoadSession: true, SessionCapabilities: acp.SessionCapabilities{List: &acp.SessionListCapabilities{}},
			PromptCapabilities: acp.PromptCapabilities{EmbeddedContext: os.Getenv("FAKE_PLAIN") == "", Image: os.Getenv("FAKE_PLAIN") == ""}},
		AgentInfo: &acp.Implementation{Name: "fake-agent", Version: "1"},
	}, nil
}

func (a *fakeAgent) Authenticate(ctx context.Context, p acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *fakeAgent) Logout(ctx context.Context, p acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (a *fakeAgent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	n := a.sessions.Add(1)
	return acp.NewSessionResponse{SessionId: acp.SessionId(fmt.Sprintf("s%d-%d", os.Getpid(), n))}, nil
}

func (a *fakeAgent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	// What codex-acp answers for a conversation another client has open.
	if strings.HasPrefix(string(p.SessionId), "busy-") {
		return acp.LoadSessionResponse{}, &acp.RequestError{Code: -32600,
			Message: "Invalid request: This Codex session is in use by another Codex client (the Codex app, the CLI or an IDE extension). Close the session there or quit that client, then try again.",
			Data:    map[string]any{"reason": "thread_active_writer", "threadId": string(p.SessionId)}}
	}
	// A loaded session keeps its prompt count across processes through an env-named file.
	if dir := os.Getenv("FAKE_STATE"); dir != "" {
		if b, err := os.ReadFile(dir + "/" + string(p.SessionId)); err == nil {
			n, _ := strconv.Atoi(string(b))
			a.mu.Lock()
			a.prompts[p.SessionId] = n
			a.mu.Unlock()
			return acp.LoadSessionResponse{}, nil
		}
	}
	return acp.LoadSessionResponse{}, acp.NewInvalidParams("unknown session")
}

func (a *fakeAgent) Cancel(ctx context.Context, p acp.CancelNotification) error {
	a.mu.Lock()
	if c := a.cancels[p.SessionId]; c != nil {
		c()
	}
	a.mu.Unlock()
	return nil
}

func (a *fakeAgent) SetSessionMode(ctx context.Context, p acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (a *fakeAgent) say(ctx context.Context, sid acp.SessionId, text string) {
	_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateAgentMessageText(text)})
}

func (a *fakeAgent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	var text strings.Builder
	for _, b := range p.Prompt {
		if b.Text != nil {
			text.WriteString(b.Text.Text)
		}
	}
	full := text.String()
	lines := strings.Split(strings.TrimSpace(full), "\n")
	cmd, arg, _ := strings.Cut(strings.TrimSpace(lines[len(lines)-1]), " ")

	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.prompts[p.SessionId]++
	count := a.prompts[p.SessionId]
	a.cancels[p.SessionId] = cancel
	a.mu.Unlock()
	defer cancel()
	if dir := os.Getenv("FAKE_STATE"); dir != "" {
		_ = os.WriteFile(dir+"/"+string(p.SessionId), []byte(strconv.Itoa(count)), 0o600)
	}
	sid := p.SessionId
	switch cmd {
	case "echo":
		a.say(ctx, sid, arg)
	case "tools":
		a.say(ctx, sid, "thinking")
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.StartToolCall("t1", "read file", acp.WithStartKind(acp.ToolKindRead))})
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateToolCall("t1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted))})
		a.say(ctx, sid, "final")
	case "permission":
		resp, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
			SessionId: sid,
			ToolCall:  acp.ToolCallUpdate{ToolCallId: "t2", Title: acp.Ptr("rm -rf build")},
			Options: []acp.PermissionOption{
				{OptionId: "yes", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
				{OptionId: "no", Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce},
			},
		})
		if err == nil && resp.Outcome.Selected != nil && resp.Outcome.Selected.OptionId == "yes" {
			a.say(ctx, sid, "allowed")
		} else {
			a.say(ctx, sid, "denied")
		}
	case "sleep":
		ms, _ := strconv.Atoi(arg)
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
			a.say(ctx, sid, "slept")
		case <-ctx.Done():
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
	case "stream":
		var count, ms int
		fmt.Sscan(arg, &count, &ms)
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateAgentThoughtText("hmm")})
		for i := 1; i <= count; i++ {
			word := "w" + strconv.Itoa(i)
			if i < count {
				word += " "
			}
			a.say(ctx, sid, word)
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-ctx.Done():
				return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
			}
		}
	case "crash":
		a.say(ctx, sid, "about to crash")
		os.Exit(7)
	case "count":
		a.say(ctx, sid, strconv.Itoa(count))
	case "env":
		a.say(ctx, sid, os.Getenv(arg))
	case "big":
		n, _ := strconv.Atoi(arg)
		for n > 0 {
			chunk := min(n, 64<<10)
			a.say(ctx, sid, strings.Repeat("b", chunk))
			n -= chunk
		}
	case "refuse":
		a.say(ctx, sid, "no")
		return acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	case "prompt":
		a.say(ctx, sid, full)
	case "shell":
		out, err := exec.CommandContext(ctx, "sh", "-c", arg).CombinedOutput()
		if err != nil {
			out = append(out, []byte("\nerror: "+err.Error())...)
		}
		a.say(ctx, sid, string(out))
	case "sessions":
		a.say(ctx, sid, strconv.FormatInt(a.sessions.Load(), 10))
	case "give":
		send := func(c acp.ContentBlock) {
			_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.SessionUpdate{
				AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: c, SessionUpdate: "agent_message_chunk"}}})
		}
		if size, err := strconv.Atoi(arg); err == nil {
			a.say(ctx, sid, strings.Repeat("x", size))
		} else {
			a.say(ctx, sid, "here")
		}
		send(acp.ImageBlock("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "image/png"))
		send(acp.ResourceLinkBlock("spec.pdf", "https://example.com/download?id=42"))
		if disk := os.Getenv("FAKE_DISK"); disk != "" {
			data, _ := os.ReadFile(disk)
			send(acp.ResourceBlock(acp.EmbeddedResourceResource{TextResourceContents: &acp.TextResourceContents{Uri: "file://" + disk, Text: string(data)}}))
		}
		send(acp.ResourceBlock(acp.EmbeddedResourceResource{TextResourceContents: &acp.TextResourceContents{Uri: "file:///nowhere/new.txt", Text: "fresh"}}))
		if link := os.Getenv("FAKE_LINK"); link != "" {
			send(acp.ResourceLinkBlock("linked.txt", "file://"+link))
		}
		for _, c := range p.Prompt {
			if c.Resource != nil && c.Resource.Resource.TextResourceContents != nil {
				send(acp.ResourceLinkBlock("", c.Resource.Resource.TextResourceContents.Uri))
			}
		}
	case "edit":
		// Each resource it was given, handed back under the same URI with changed text.
		a.say(ctx, sid, "edited")
		for _, c := range p.Prompt {
			if r := c.Resource; r != nil && r.Resource.TextResourceContents != nil {
				_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.SessionUpdate{
					AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{SessionUpdate: "agent_message_chunk",
						Content: acp.ResourceBlock(acp.EmbeddedResourceResource{TextResourceContents: &acp.TextResourceContents{
							Uri: r.Resource.TextResourceContents.Uri, Text: r.Resource.TextResourceContents.Text + " (fixed)"}})}}})
			}
		}
	case "blocks":
		var b strings.Builder
		for _, c := range p.Prompt {
			switch {
			case c.Resource != nil && c.Resource.Resource.TextResourceContents != nil:
				r := c.Resource.Resource.TextResourceContents
				fmt.Fprintf(&b, "resource %s %q\n", r.Uri, r.Text)
			case c.Resource != nil && c.Resource.Resource.BlobResourceContents != nil:
				fmt.Fprintf(&b, "blob %s\n", c.Resource.Resource.BlobResourceContents.Uri)
			case c.Image != nil:
				fmt.Fprintf(&b, "image %s %d\n", c.Image.MimeType, len(c.Image.Data))
			case c.ResourceLink != nil:
				data, err := os.ReadFile(strings.TrimPrefix(c.ResourceLink.Uri, "file://"))
				fmt.Fprintf(&b, "link %s %q %v\n", c.ResourceLink.Name, data, err)
			}
		}
		a.say(ctx, sid, b.String())
	case "demo": // a turn with everything a conversation shows, paced like an agent
		up := func(u acp.SessionUpdate) {
			_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: u})
		}
		pause := func(ms int) {
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-ctx.Done():
			}
		}
		up(acp.UpdateAgentThoughtText("The test fails on an empty config; look at the parser first."))
		pause(600)
		plan := func(st ...acp.PlanEntryStatus) {
			names := []string{"Find why config parsing fails", "Fix the parser", "Run the tests"}
			var es []acp.PlanEntry
			for i, n := range names {
				es = append(es, acp.PlanEntry{Content: n, Status: st[i], Priority: acp.PlanEntryPriorityMedium})
			}
			up(acp.SessionUpdate{Plan: &acp.SessionUpdatePlan{SessionUpdate: "plan", Entries: es}})
		}
		plan(acp.PlanEntryStatusInProgress, acp.PlanEntryStatusPending, acp.PlanEntryStatusPending)
		a.say(ctx, sid, "Let me look at how the config is read.\n\n")
		up(acp.StartToolCall("d1", "Read config.go", acp.WithStartKind(acp.ToolKindRead), acp.WithStartLocations([]acp.ToolCallLocation{{Path: "internal/config/config.go"}}), acp.WithStartRawInput(map[string]any{"file_path": "internal/config/config.go"})))
		pause(400)
		up(acp.UpdateToolCall("d1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
		up(acp.StartToolCall("d2", "Terminal", acp.WithStartKind(acp.ToolKindExecute), acp.WithStartRawInput(map[string]any{"command": "go test ./internal/config", "description": "Run the config package's tests"})))
		pause(900)
		up(acp.UpdateToolCall("d2", acp.WithUpdateStatus(acp.ToolCallStatusFailed), acp.WithUpdateRawOutput(map[string]any{"output": "--- FAIL: TestEmpty (0.00s)\n    config_test.go:41: unexpected EOF\nFAIL", "exit_code": 1})))
		plan(acp.PlanEntryStatusCompleted, acp.PlanEntryStatusInProgress, acp.PlanEntryStatusPending)
		up(acp.StartToolCall("d3", "Edit config.go", acp.WithStartKind(acp.ToolKindEdit), acp.WithStartContent([]acp.ToolCallContent{acp.ToolDiffContent("internal/config/config.go", "\tif len(bytes.TrimSpace(b)) == 0 {\n\t\treturn &Config{}, nil // an empty file is the defaults\n\t}\n\treturn parse(b)", "\tif len(b) == 0 {\n\t\treturn nil, io.ErrUnexpectedEOF\n\t}\n\treturn parse(b)")})))
		pause(500)
		up(acp.UpdateToolCall("d3", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
		up(acp.StartToolCall("d4", "Terminal", acp.WithStartKind(acp.ToolKindExecute), acp.WithStartRawInput(map[string]any{"command": "go test ./internal/config", "description": "Run the tests again"})))
		pause(700)
		up(acp.UpdateToolCall("d4", acp.WithUpdateStatus(acp.ToolCallStatusCompleted), acp.WithUpdateRawOutput(map[string]any{"output": "ok  \tgithub.com/k1dory/acpost/internal/config\t0.41s", "exit_code": 0})))
		plan(acp.PlanEntryStatusCompleted, acp.PlanEntryStatusCompleted, acp.PlanEntryStatusCompleted)
		used, size := 12400, 32000
		up(acp.SessionUpdate{UsageUpdate: &acp.SessionUsageUpdate{SessionUpdate: "usage_update", Used: used, Size: size, Cost: &acp.Cost{Amount: 0.042, Currency: "USD"}}})
		for _, w := range strings.Fields("Fixed. An **empty config file** now means the defaults instead of failing with `unexpected EOF`:") {
			a.say(ctx, sid, w+" ")
			pause(40)
		}
		a.say(ctx, sid, "\n\n- `internal/config/config.go`: whitespace-only input returns `&Config{}`\n- `go test ./internal/config` passes")
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn, Usage: &acp.Usage{InputTokens: 1840, OutputTokens: 312, TotalTokens: 14550, CachedReadTokens: acp.Ptr(12398)}}, nil
	case "usage": // answered below, with the response
	case "costly":
		var total float64
		var ms int
		fmt.Sscan(arg, &total, &ms)
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.SessionUpdate{
			UsageUpdate: &acp.SessionUsageUpdate{SessionUpdate: "usage_update", Cost: &acp.Cost{Amount: total, Currency: "USD"}}}})
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
		case <-ctx.Done():
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
	default:
		a.say(ctx, sid, "unknown: "+cmd)
	}
	if cmd == "usage" {
		var in, out, cr, cw int
		var total float64
		n, _ := fmt.Sscan(arg, &in, &out, &cr, &cw, &total)
		if n == 5 {
			_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.SessionUpdate{
				UsageUpdate: &acp.SessionUsageUpdate{SessionUpdate: "usage_update", Used: in + cr, Size: 200000,
					Cost: &acp.Cost{Amount: total, Currency: "USD"}}}})
		}
		a.say(ctx, sid, "used")
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn, Usage: &acp.Usage{InputTokens: in, OutputTokens: out,
			CachedReadTokens: &cr, CachedWriteTokens: &cw, TotalTokens: in + out + cr + cw}}, nil
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *fakeAgent) CloseSession(ctx context.Context, p acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

// ListSessions lists the sessions kept in FAKE_STATE, newest first by file time.
func (a *fakeAgent) ListSessions(ctx context.Context, p acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	var out acp.ListSessionsResponse
	dir := os.Getenv("FAKE_STATE")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out.Sessions = append(out.Sessions, acp.SessionInfo{SessionId: acp.SessionId(e.Name()), Cwd: "/fake",
			Title: acp.Ptr("session " + e.Name()), UpdatedAt: acp.Ptr(info.ModTime().UTC().Format(time.RFC3339Nano))})
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return *out.Sessions[i].UpdatedAt > *out.Sessions[j].UpdatedAt })
	return out, nil
}

func (a *fakeAgent) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, acp.NewMethodNotFound("session/resume")
}

func (a *fakeAgent) SetSessionConfigOption(ctx context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

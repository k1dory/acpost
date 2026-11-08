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
//
// Knobs in its environment: FAKE_HANG_NEW (session/new never answers), FAKE_EXCLUSIVE (with
// FAKE_STATE: a session open in another live process is busy, as codex-acp's), FAKE_ONE_PROMPT=DIR
// (a prompt into a session another prompt is in, in any process, fails), FAKE_FAIL_NEW_ONCE=FILE
// (the first session/new across processes fails; FILE marks it done).
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
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type fakeAgent struct {
	conn     *acp.AgentSideConnection
	mu       sync.Mutex
	prompts  map[acp.SessionId]int
	cancels  map[acp.SessionId]context.CancelFunc
	sessions atomic.Int64
	cwds     map[acp.SessionId]string
	mcp      map[acp.SessionId]string            // the MCP servers the session was given
	settings map[acp.SessionId]map[string]string // config id → value: model, effort
}

// options are the session's configuration options as it stands: a model and a reasoning level.
func (a *fakeAgent) options(sid acp.SessionId) []acp.SessionConfigOption {
	a.mu.Lock()
	cur := a.settings[sid]
	a.mu.Unlock()
	opt := func(id, name string, cat acp.SessionConfigOptionCategory, values ...string) acp.SessionConfigOption {
		var vs acp.SessionConfigSelectOptionsUngrouped
		for _, v := range values {
			vs = append(vs, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(v), Name: strings.ToUpper(v[:1]) + v[1:]})
		}
		current := values[0]
		if c := cur[id]; c != "" {
			current = c
		}
		o := acp.NewSessionConfigOptionSelect(acp.SessionConfigValueId(current), acp.SessionConfigSelectOptions{Ungrouped: &vs})
		o.Select.Id, o.Select.Name, o.Select.Category = acp.SessionConfigId(id), name, &cat
		return o
	}
	out := []acp.SessionConfigOption{
		opt("model", "Model", acp.SessionConfigOptionCategoryModel, "fake-small", "fake-big"),
		opt("effort", "Reasoning", acp.SessionConfigOptionCategoryThoughtLevel, "low", "high"),
	}
	// FAKE_MODES: modes as Claude Code has them (FAKE_MODES=broken: setting one fails).
	if os.Getenv("FAKE_MODES") != "" {
		out = append(out, opt("mode", "Mode", acp.SessionConfigOptionCategoryMode, "default", "plan"))
	}
	return out
}

// Run serves the scripted agent on stdin/stdout until the client goes, then exits.
func Run() {
	a := &fakeAgent{prompts: map[acp.SessionId]int{}, cancels: map[acp.SessionId]context.CancelFunc{},
		cwds: map[acp.SessionId]string{}, settings: map[acp.SessionId]map[string]string{}, mcp: map[acp.SessionId]string{}}
	a.conn = acp.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	<-a.conn.Done()
	os.Exit(0)
}

func (a *fakeAgent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{LoadSession: true, SessionCapabilities: acp.SessionCapabilities{List: &acp.SessionListCapabilities{}, Fork: &acp.SessionForkCapabilities{}},
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
	if os.Getenv("FAKE_HANG_NEW") != "" {
		<-ctx.Done()
		return acp.NewSessionResponse{}, ctx.Err()
	}
	if mark := os.Getenv("FAKE_FAIL_NEW_ONCE"); mark != "" {
		if f, err := os.OpenFile(mark, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
			f.Close()
			return acp.NewSessionResponse{}, acp.NewInternalError(map[string]any{"why": "an MCP server did not start"})
		}
	}
	n := a.sessions.Add(1)
	sid := acp.SessionId(fmt.Sprintf("s%d-%d", os.Getpid(), n))
	a.mu.Lock()
	a.cwds[sid] = p.Cwd
	var mcp []string
	for _, m := range p.McpServers {
		switch {
		case m.Stdio != nil:
			var env []string
			for _, e := range m.Stdio.Env {
				env = append(env, e.Name+"="+e.Value)
			}
			mcp = append(mcp, m.Stdio.Name+":"+m.Stdio.Command+":"+strings.Join(env, ","))
		case m.Http != nil:
			mcp = append(mcp, m.Http.Name+":http")
		}
	}
	a.mcp[sid] = strings.Join(mcp, ";")
	a.mu.Unlock()
	if os.Getenv("FAKE_COMMANDS") != "" {
		go func() { // what claude-agent-acp does after session/new
			time.Sleep(10 * time.Millisecond)
			_ = a.conn.SessionUpdate(context.Background(), acp.SessionNotification{SessionId: sid, Update: acp.SessionUpdate{
				AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{SessionUpdate: "available_commands_update",
					AvailableCommands: []acp.AvailableCommand{{Name: "review", Description: "Review the changes"}}}}})
		}()
	}
	return acp.NewSessionResponse{SessionId: sid, ConfigOptions: a.options(sid)}, nil
}

// UnstableForkSession forks a session: a hist- one into another hist- (the same history replayed).
func (a *fakeAgent) UnstableForkSession(ctx context.Context, p acp.UnstableForkSessionRequest) (acp.UnstableForkSessionResponse, error) {
	if strings.HasPrefix(string(p.SessionId), "busy-") {
		return acp.UnstableForkSessionResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": p.SessionId})
	}
	n := a.sessions.Add(1)
	sid := acp.SessionId(fmt.Sprintf("hist-fork%d-%d", os.Getpid(), n))
	a.mu.Lock()
	a.cwds[sid] = p.Cwd
	a.mu.Unlock()
	return acp.UnstableForkSessionResponse{SessionId: sid}, nil
}

func (a *fakeAgent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	// What codex-acp answers for a conversation another client has open.
	if strings.HasPrefix(string(p.SessionId), "busy-") {
		return acp.LoadSessionResponse{}, &acp.RequestError{Code: -32600,
			Message: "Invalid request: This Codex session is in use by another Codex client (the Codex app, the CLI or an IDE extension). Close the session there or quit that client, then try again.",
			Data:    map[string]any{"reason": "thread_active_writer", "threadId": string(p.SessionId)}}
	}
	// A session with a history: replayed as updates before the answer, as agents do.
	if strings.HasPrefix(string(p.SessionId), "hist-") {
		sid := p.SessionId
		up := func(u acp.SessionUpdate) {
			_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: u})
		}
		up(acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{SessionUpdate: "user_message_chunk", Content: acp.TextBlock("what is in config.go?")}})
		up(acp.UpdateAgentThoughtText("look at the file"))
		up(acp.StartToolCall("h1", "Read config.go", acp.WithStartKind(acp.ToolKindRead)))
		up(acp.UpdateToolCall("h1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
		up(acp.UpdateAgentMessageText("It parses the YAML."))
		up(acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{SessionUpdate: "user_message_chunk", Content: acp.TextBlock("thanks")}})
		up(acp.UpdateAgentMessageText("You're welcome."))
		a.mu.Lock()
		a.cwds[sid] = p.Cwd
		a.mu.Unlock()
		return acp.LoadSessionResponse{ConfigOptions: a.options(sid)}, nil
	}
	// A loaded session keeps its prompt count across processes through an env-named file.
	if dir := os.Getenv("FAKE_STATE"); dir != "" {
		if os.Getenv("FAKE_EXCLUSIVE") != "" {
			// codex-like: a session open in another live process is busy.
			lock := dir + "/" + string(p.SessionId) + ".lock"
			if b, err := os.ReadFile(lock); err == nil {
				pid, _ := strconv.Atoi(string(b))
				if pid != os.Getpid() && pid > 0 && syscall.Kill(pid, 0) == nil {
					return acp.LoadSessionResponse{}, &acp.RequestError{Code: -32600, Message: "Invalid request: This Codex session is in use by another Codex client", Data: map[string]any{"reason": "thread_active_writer"}}
				}
			}
			_ = os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o600)
		}
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

	if dir := os.Getenv("FAKE_ONE_PROMPT"); dir != "" {
		// One prompt at a time in a session, across processes: a second one is an error.
		active := dir + "/" + string(p.SessionId) + ".active"
		f, err := os.OpenFile(active, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"why": "two prompts at once in session " + string(p.SessionId)})
		}
		f.Close()
		defer os.Remove(active)
	}
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.prompts[p.SessionId]++
	count := a.prompts[p.SessionId]
	a.cancels[p.SessionId] = cancel
	a.mu.Unlock()
	defer cancel()
	if dir := os.Getenv("FAKE_STATE"); dir != "" {
		_ = os.WriteFile(dir+"/"+string(p.SessionId), []byte(strconv.Itoa(count)), 0o600)
		if os.Getenv("FAKE_EXCLUSIVE") != "" {
			_ = os.WriteFile(dir+"/"+string(p.SessionId)+".lock", []byte(strconv.Itoa(os.Getpid())), 0o600)
		}
	}
	sid := p.SessionId
	if strings.Contains(full, "Write a wiki for the repository") { // a wiki: pages in its format
		a.mu.Lock()
		dir := a.cwds[sid]
		a.mu.Unlock()
		a.say(ctx, sid, "Here it is.\n<<<PAGE slug=\"overview\" title=\"Overview\">>>\nThe code in "+dir+".\n<<<END>>>\n"+
			"<<<PAGE slug=\"store\" title=\"Store\" parent=\"architecture\">>>\nSQLite.\n```mermaid\ngraph TD; a-->b\n```\n<<<END>>>\n"+
			"<<<PAGE slug=\"overview\" title=\"Again\">>>\nduplicate\n<<<END>>>")
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	switch cmd {
	case "echo":
		a.say(ctx, sid, arg)
	case "mcp": // the MCP servers the session was given
		a.mu.Lock()
		got := a.mcp[sid]
		a.mu.Unlock()
		a.say(ctx, sid, got)
	case "cwd": // the session's directory
		a.mu.Lock()
		dir := a.cwds[sid]
		a.mu.Unlock()
		a.say(ctx, sid, dir)
	case "settings": // the session's model and reasoning level
		var b strings.Builder
		for _, o := range a.options(sid) {
			fmt.Fprintf(&b, "%s=%s ", o.Select.Id, o.Select.CurrentValue)
		}
		a.say(ctx, sid, strings.TrimSpace(b.String()))
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
	v := p.ValueId
	if v == nil {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams("a value")
	}
	if v.ConfigId == "mode" && os.Getenv("FAKE_MODES") == "broken" {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInternalError(map[string]any{"why": "no modes today"})
	}
	if v.ConfigId != "model" && v.ConfigId != "effort" && (v.ConfigId != "mode" || os.Getenv("FAKE_MODES") == "") {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams("no such option")
	}
	a.mu.Lock()
	if a.settings[v.SessionId] == nil {
		a.settings[v.SessionId] = map[string]string{}
	}
	a.settings[v.SessionId][string(v.ConfigId)] = string(v.Value)
	a.mu.Unlock()
	return acp.SetSessionConfigOptionResponse{ConfigOptions: a.options(v.SessionId)}, nil
}

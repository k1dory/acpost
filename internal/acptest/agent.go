// Package acptest is a scripted ACP agent for tests. Run serves ACP on stdin/stdout. What it does is driven by the prompt's last line:
//
//	echo TEXT        answers TEXT
//	tools            says "thinking", makes a tool call, then answers "final"
//	permission       asks permission for a tool call; answers "allowed" or "denied"
//	sleep MS         takes MS milliseconds (or until cancelled)
//	crash            exits mid-turn
//	count            answers how many prompts its session has had
//	env NAME         answers the value of $NAME
//	big N            answers N bytes
//	refuse           ends the turn with stopReason refusal
//	prompt           answers the whole prompt it got
//	sessions         answers how many sessions this process created
//	shell CMD        runs CMD with sh -c and answers its combined output
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
		ProtocolVersion:   acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{LoadSession: true, SessionCapabilities: acp.SessionCapabilities{List: &acp.SessionListCapabilities{}}},
		AgentInfo:         &acp.Implementation{Name: "fake-agent", Version: "1"},
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
	default:
		a.say(ctx, sid, "unknown: "+cmd)
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

package node

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
	"github.com/k1dory/acpx"
)

// driver: sandbox — a cloud agent: a sandbox of agent-sandbox (sandboxd). A message is the next
// message of the sandbox's conversation (POST /v1/sandboxes/{id}/messages, its files as uploads);
// what the agent does comes back on the conversation's stream (GET …/messages/stream) and becomes
// the message's events (text, thoughts, tool calls, plan, usage) and its reply, as a local agent's
// would. The turn ends with the agent's turn (acp.turn_end, a result line) or its run.
//
// A cloud agent runs without asking: the sandbox, its policy and the gateway are its boundary
// (the sandbox's harness.permissions decides; allow by default). When the sandbox does ask
// (permissions: ask there), its requests are this message's permission requests, answered as any
// other (acpost approve, the app) and handed to the sandbox.

const (
	sandboxParts    = 8        // file parts a message takes (sandboxd's limit)
	sandboxPartMax  = 16 << 20 // bytes of one
	sandboxCatchUp  = 5000     // events scanned per page while finding where the stream stands
	sandboxFrameMax = 4 << 20  // bytes of one SSE frame
)

type sandboxAgent struct {
	n    *Node
	name string
	addr string
	cfg  *config.Agent
	api  *http.Client // calls, bounded
	sse  *http.Client // the stream, open as long as a turn runs
	// cursor is the last event of the sandbox's stream seen (0: not known yet).
	cursor atomic.Int64
	busy   atomic.Bool
}

func newSandboxAgent(n *Node, name string, cfg *config.Agent) (*sandboxAgent, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ca := cfg.Sandbox.CA; ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, fmt.Errorf("agents.%s.sandbox.ca: %w", name, err)
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("agents.%s.sandbox.ca: no certificate in %s", name, ca)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse } // the token stays with sandboxd
	return &sandboxAgent{n: n, name: name, addr: "agent:" + name, cfg: cfg,
		api: &http.Client{Transport: tr, Timeout: 2 * time.Minute, CheckRedirect: noRedirect},
		sse: &http.Client{Transport: tr, CheckRedirect: noRedirect}}, nil
}

func (a *sandboxAgent) start() { a.n.goLoop(a.loop) }

func (a *sandboxAgent) loop() {
	n := a.n
	for n.ctx.Err() == nil {
		m, ok, err := n.claim(n.ctx, []string{a.addr}, time.Second, a.cfg.Lease.D())
		if err != nil {
			if n.ctx.Err() == nil {
				n.log.Warn("claim", "agent", a.name, "err", err)
				time.Sleep(time.Second)
			}
			continue
		}
		if ok {
			a.busy.Store(true)
			a.handle(m)
			a.busy.Store(false)
		}
	}
}

// sandboxError is sandboxd's refusal, or a failure to reach it.
type sandboxError struct {
	status int
	msg    string
}

func (e *sandboxError) Error() string { return e.msg }

// retryable: sandboxd did not answer, or answered that it could not now.
func (e *sandboxError) retryable() bool {
	return e.status == 0 || e.status == http.StatusTooManyRequests || e.status >= 500
}

func (a *sandboxAgent) path(p string) string {
	return a.cfg.Sandbox.URL + "/v1/sandboxes/" + url.PathEscape(a.cfg.Sandbox.ID) + p
}

func (a *sandboxAgent) call(ctx context.Context, method, p string, hdr http.Header, body io.Reader, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.path(p), body)
	if err != nil {
		return nil, &sandboxError{msg: err.Error()}
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.Sandbox.Token)
	resp, err := a.api.Do(req)
	if err != nil {
		return nil, &sandboxError{msg: fmt.Sprintf("sandboxd: %v", err)}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b[:min(len(b), 300)]))
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &e) == nil && (e.Message != "" || e.Error != "") {
			msg = strings.TrimSpace(e.Error + " " + e.Message)
		}
		return resp.Header, &sandboxError{status: resp.StatusCode, msg: fmt.Sprintf("sandboxd %s %s: %d %s", method, p, resp.StatusCode, msg)}
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.Header, &sandboxError{status: resp.StatusCode, msg: fmt.Sprintf("sandboxd %s: not the API's answer (%v)", p, err)}
		}
	}
	return resp.Header, nil
}

// sandboxMessage is one turn of the sandbox's conversation.
type sandboxMessage struct {
	ID    int64           `json:"id"`
	RunID string          `json:"run_id"`
	Role  string          `json:"role"`
	Text  string          `json:"text"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type sandboxRun struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Session string `json:"session,omitempty"`
}

// catchUp moves the cursor to where the sandbox's stream stands now: what is before it is not this
// turn's. Each turn catches up from where the last one left (the first, from the start).
func (a *sandboxAgent) catchUp(ctx context.Context) error {
	after := a.cursor.Load()
	for range 1000 {
		h, err := a.call(ctx, "GET", "/messages?raw=true&limit="+strconv.Itoa(sandboxCatchUp)+"&after="+strconv.FormatInt(after, 10), nil, nil, nil)
		if err != nil {
			return err
		}
		next, _ := strconv.ParseInt(h.Get("X-Next-Cursor"), 10, 64)
		if next <= after {
			break
		}
		after = next
	}
	a.seen(after)
	return nil
}

// seen moves the cursor forward (never back).
func (a *sandboxAgent) seen(id int64) {
	for {
		cur := a.cursor.Load()
		if id <= cur || a.cursor.CompareAndSwap(cur, id) {
			return
		}
	}
}

// post sends the message: its text, its files as uploads (links are named in the text).
func (a *sandboxAgent) post(ctx context.Context, m store.Message) (sandboxRun, string, string, error) {
	var files []store.File
	if len(m.Files) > 0 {
		all, err := a.n.st.FilesOf(ctx, m.ID)
		if err != nil {
			return sandboxRun{}, "", "", err
		}
		files = pick(all, false)
	}
	var text strings.Builder
	var uploads []store.File
	for _, f := range files {
		if f.Link() {
			fmt.Fprintf(&text, "[acpost] Attached: %s (%s), a link: %s\n", f.Name, f.Type, f.URI)
			continue
		}
		if len(uploads) >= sandboxParts || len(f.Data) > sandboxPartMax {
			fmt.Fprintf(&text, "[acpost] Not attached (the sandbox takes %d files of at most 16 MiB): %s\n", sandboxParts, f.Name)
			continue
		}
		uploads = append(uploads, f)
	}
	if text.Len() > 0 {
		text.WriteString("\n")
	}
	if a.cfg.PreambleOn() {
		fmt.Fprintf(&text, "[acpost] Message %s from %s", m.ID, m.From)
		if m.Task != "" {
			fmt.Fprintf(&text, ", task %s", m.Task)
		}
		text.WriteString(". Your final answer in this turn is sent back to the sender as the reply.\n\n")
	}
	text.WriteString(m.Body)

	hdr := http.Header{}
	// The message posted again (sandboxd's answer was lost, the node stopped) is the same message
	// there: never a second run of it.
	hdr.Set("Idempotency-Key", "acpost-"+m.ID)
	var body bytes.Buffer
	if len(uploads) == 0 {
		hdr.Set("Content-Type", "application/json")
		_ = json.NewEncoder(&body).Encode(map[string]any{"text": text.String()})
	} else {
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("text", text.String())
		for _, f := range uploads {
			w, err := mw.CreateFormFile("file", f.Name)
			if err != nil {
				return sandboxRun{}, "", "", err
			}
			_, _ = w.Write(f.Data)
		}
		_ = mw.Close()
		hdr.Set("Content-Type", mw.FormDataContentType())
	}
	var out struct {
		Run      sandboxRun `json:"run"`
		Delivery string     `json:"delivery"`
	}
	if _, err := a.call(ctx, "POST", "/messages", hdr, &body, &out); err != nil {
		return sandboxRun{}, "", "", err
	}
	if out.Run.ID == "" {
		return sandboxRun{}, "", "", &sandboxError{status: 200, msg: "sandboxd: no run in its answer"}
	}
	return out.Run, out.Delivery, text.String(), nil
}

// sandboxTurn is what one turn has said so far.
type sandboxTurn struct {
	a      *sandboxAgent
	msgID  string
	run    sandboxRun
	stream *textStream
	limit  int
	mu     sync.Mutex
	all    strings.Builder
	last   strings.Builder
	usage  store.Usage
	used   bool
	// asks are the sandbox's requests waiting here, by the sandbox's request id.
	asks map[string]*sandboxAsk
	// after is the last event of the stream this turn has read.
	after int64
	// The turn is this message's from the conversation's turn that carries it: its text begins
	// with mark. Before it, the run's output is an earlier turn's.
	mark    string
	started bool
}

// sandboxAsk is a request of the sandbox waiting here: the id it has here, and the end of its wait.
type sandboxAsk struct {
	local string
	stop  context.CancelFunc
}

func (t *sandboxTurn) text(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.all.Len() > 0 && !strings.HasSuffix(t.all.String(), "\n") {
		s = "\n\n" + s // one message after another: apart
	}
	if t.all.Len() <= t.limit {
		t.all.WriteString(s)
	}
	if t.last.Len() <= t.limit {
		t.last.WriteString(strings.TrimLeft(s, "\n"))
	}
	t.stream.add("text", s)
}

func (t *sandboxTurn) reply(mode string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if mode == "last" && strings.TrimSpace(t.last.String()) != "" {
		return t.last.String()
	}
	return t.all.String()
}

// end is how the turn ended: "" while it runs.
type sandboxEnd struct {
	state  store.State
	reason string // stop reason, or the run's end
}

// take reads one turn of the conversation into the message's events; end says the turn is over.
func (t *sandboxTurn) take(ctx context.Context, sm sandboxMessage) (sandboxEnd, bool) {
	if !t.started {
		switch {
		case sm.Role == "user" && strings.HasPrefix(strings.TrimSpace(sm.Text), t.mark):
			t.started = true
		case sm.Role == "system" && strings.HasPrefix(sm.Text, "run "):
			return sandboxEnd{store.Failed, sm.Text + " before it took the message"}, true
		}
		return sandboxEnd{}, false // an earlier turn of the run, or this one's own message
	}
	switch sm.Role {
	case "assistant":
		if s := strings.TrimSpace(sm.Text); s != "" {
			t.text(s)
		}
	case "tool":
		// What the agent set out to do, as the tool call's title says it.
		t.mu.Lock()
		t.last.Reset()
		t.mu.Unlock()
		t.stream.event("tool_call", toolEvent{ID: "sandbox-" + strconv.FormatInt(sm.ID, 10), Title: redact(cutString(sm.Text, 512)), Status: "pending"})
	case "request":
		t.ask(ctx, sm)
	case "resolved":
		var d struct {
			RequestID string `json:"request_id"`
			Outcome   string `json:"outcome"`
			By        string `json:"by"`
		}
		_ = json.Unmarshal(sm.Data, &d)
		id := d.RequestID
		t.mu.Lock()
		if w := t.asks[d.RequestID]; w != nil {
			w.stop()
			id = w.local // as the permission_request said it
			delete(t.asks, d.RequestID)
		}
		t.mu.Unlock()
		t.stream.event("permission", map[string]any{"request_id": id, "decision": d.Outcome, "by": "sandbox:" + d.By})
	case "commands":
		t.stream.event("commands", json.RawMessage(sm.Data))
	case "system":
		switch sm.Text {
		case "run done":
			return sandboxEnd{store.Done, "run done"}, true
		case "run failed", "run cancelled":
			return sandboxEnd{store.Failed, sm.Text}, true
		default:
			t.stream.event("notice", map[string]string{"text": sm.Text})
		}
	case "log":
		return t.line(sm.Text)
	}
	return sandboxEnd{}, false
}

// line reads a harness output line that is not a turn: the turn's end, usage, thoughts, plan.
func (t *sandboxTurn) line(s string) (sandboxEnd, bool) {
	var l struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Subtype    string `json:"subtype"`
		IsError    bool   `json:"is_error"`
		Text       string `json:"text"`
		Title      string `json:"title"`
		Severity   string `json:"severity"`
		Entries    []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"entries"`
		// usage
		Model        string  `json:"model"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		CacheRead    int64   `json:"cache_read_tokens"`
		CacheWrite   int64   `json:"cache_write_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}
	if !strings.HasPrefix(strings.TrimSpace(s), "{") || json.Unmarshal([]byte(s), &l) != nil {
		return sandboxEnd{}, false
	}
	switch l.Type {
	case "acp.turn_end":
		if l.StopReason == "" || l.StopReason == "end_turn" {
			return sandboxEnd{store.Done, "end_turn"}, true
		}
		return sandboxEnd{store.Failed, l.StopReason}, true
	case "turn.completed": // Codex
		return sandboxEnd{store.Done, "turn completed"}, true
	case "result": // Claude Code
		if l.IsError {
			return sandboxEnd{store.Failed, cmpOr(l.Subtype, "error")}, true
		}
		return sandboxEnd{store.Done, "result"}, true
	case "usage":
		t.mu.Lock()
		t.used = true
		u := &t.usage
		if l.Model != "" {
			u.Model = l.Model
		}
		u.Input += int(l.InputTokens)
		u.Output += int(l.OutputTokens)
		u.CacheRead += int(l.CacheRead)
		u.CacheWrite += int(l.CacheWrite)
		if l.CostUSD > 0 {
			c := l.CostUSD
			if u.Cost != nil {
				c += *u.Cost
			}
			u.Cost, u.Currency = &c, "USD"
		}
		t.mu.Unlock()
	case "acp.thought":
		t.stream.add("thought", l.Text)
	case "acp.plan":
		var entries []string
		for _, e := range l.Entries {
			entries = append(entries, e.Status+": "+e.Content)
		}
		t.stream.event("plan", entries)
	case "acp.notice":
		t.stream.event("notice", map[string]string{"severity": l.Severity, "title": l.Title, "text": redact(cutString(l.Text, 4096))})
	}
	return sandboxEnd{}, false
}

// markOf is how the conversation's turn that carries a message is known: the start of its text.
func markOf(posted string) string {
	m := strings.TrimSpace(posted)
	if len(m) > 120 {
		m = m[:120]
		for len(m) > 0 && !utf8.ValidString(m) {
			m = m[:len(m)-1]
		}
	}
	return m
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ask makes the sandbox's request a permission request of the message; a person's answer goes
// to the sandbox. An answer that does not reach it is asked again; the sandbox refuses by itself
// once its ask_timeout passes (a resolved turn then ends the wait).
func (t *sandboxTurn) ask(ctx context.Context, sm sandboxMessage) {
	var d struct {
		Request acpx.Request `json:"request"`
	}
	if json.Unmarshal(sm.Data, &d) != nil || d.Request.ID == "" {
		return
	}
	theirs := d.Request.ID
	base := d.Request
	base.Title, base.Message, base.Text = redact(base.Title), redact(base.Message), redact(base.Text)
	wctx, stop := context.WithCancel(ctx)
	w := &sandboxAsk{stop: stop}
	t.mu.Lock()
	if t.asks == nil {
		t.asks = map[string]*sandboxAsk{}
	}
	t.asks[theirs] = w
	t.mu.Unlock()
	n := t.a.n
	pending := n.askPending()
	open := func() acpx.Request {
		r := pending.Open(base)
		n.asks.mu.Lock()
		n.asks.of[r.ID] = askRef{msg: t.msgID, req: r}
		n.asks.mu.Unlock()
		t.mu.Lock()
		w.local = r.ID
		t.mu.Unlock()
		t.stream.event("permission_request", fitRequest(r))
		return r
	}
	r := open()
	go func() {
		defer stop()
		for {
			reply, ok := pending.Wait(wctx, r.ID, t.a.cfg.AskTimeout.D())
			by := n.answeredBy(r.ID)
			n.asks.mu.Lock()
			delete(n.asks.of, r.ID)
			n.asks.mu.Unlock()
			if !ok {
				return // the sandbox resolved it (its timeout, another answer), or the turn ended
			}
			err := t.a.answer(wctx, theirs, sm.RunID, reply)
			if err == nil || wctx.Err() != nil {
				return
			}
			var se *sandboxError
			if errors.As(err, &se) && se.status == http.StatusConflict {
				return // the run that asked has ended
			}
			t.stream.event("notice", map[string]string{"severity": "warning", "text": "the answer of " + by + " did not reach the sandbox (" + err.Error() + "): asked again"})
			r = open()
		}
	}()
}

// answer hands a person's answer to the sandbox, trying a few times while sandboxd cannot take it.
func (a *sandboxAgent) answer(ctx context.Context, theirs, run string, reply acpx.Reply) error {
	body := map[string]any{"run_id": run}
	if reply.OptionID != "" {
		body["option_id"] = reply.OptionID
	} else {
		body["action"] = reply.Action
		if reply.Action == acpx.ActionAccept && len(reply.Content) > 0 {
			body["content"] = reply.Content
		}
	}
	b, _ := json.Marshal(body)
	hdr := http.Header{"Content-Type": {"application/json"}}
	var err error
	for i, wait := 0, 250*time.Millisecond; i < 5; i, wait = i+1, wait*2 {
		if _, err = a.call(ctx, "POST", "/requests/"+url.PathEscape(theirs), hdr, bytes.NewReader(b), nil); err == nil {
			return nil
		}
		var se *sandboxError
		if errors.As(err, &se) && !se.retryable() {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}

// follow reads the conversation's stream after the cursor, the run's turns into t, until the turn
// ends (or ctx does). A broken stream is opened again where it stopped.
func (a *sandboxAgent) follow(ctx context.Context, t *sandboxTurn) (sandboxEnd, error) {
	backoff := 250 * time.Millisecond
	for {
		end, done, err := a.followOnce(ctx, t)
		if done {
			return end, nil
		}
		if ctx.Err() != nil {
			return sandboxEnd{}, ctx.Err()
		}
		var se *sandboxError
		if errors.As(err, &se) && !se.retryable() {
			return sandboxEnd{}, err
		}
		a.n.log.Debug("sandbox stream", "agent", a.name, "err", err)
		select {
		case <-ctx.Done():
			return sandboxEnd{}, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, 10*time.Second)
	}
}

func (a *sandboxAgent) followOnce(ctx context.Context, t *sandboxTurn) (sandboxEnd, bool, error) {
	after := t.after
	req, err := http.NewRequestWithContext(ctx, "GET", a.path("/messages/stream?raw=true&after="+strconv.FormatInt(after, 10)), nil)
	if err != nil {
		return sandboxEnd{}, false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.cfg.Sandbox.Token)
	req.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	resp, err := a.sse.Do(req)
	if err != nil {
		return sandboxEnd{}, false, &sandboxError{msg: fmt.Sprintf("sandboxd stream: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return sandboxEnd{}, false, &sandboxError{status: resp.StatusCode, msg: fmt.Sprintf("sandboxd stream: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))}
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), sandboxFrameMax)
	var event, data strings.Builder
	var id int64
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			k, v, _ := strings.Cut(line, ":")
			v = strings.TrimPrefix(v, " ")
			switch k {
			case "event":
				event.Reset()
				event.WriteString(v)
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(v)
			case "id":
				id, _ = strconv.ParseInt(v, 10, 64)
			}
			continue
		}
		ev, body := event.String(), data.String()
		event.Reset()
		data.Reset()
		if ev == "error" {
			return sandboxEnd{}, false, &sandboxError{msg: "sandboxd stream: " + cutString(body, 300)}
		}
		if body == "" {
			continue
		}
		var sm sandboxMessage
		if json.Unmarshal([]byte(body), &sm) != nil {
			continue
		}
		if sm.ID == 0 {
			sm.ID = id
		}
		if sm.ID <= t.after {
			continue // replayed
		}
		t.after = sm.ID
		a.seen(sm.ID)
		if sm.RunID != t.run.ID {
			continue
		}
		if end, done := t.take(ctx, sm); done {
			return end, true, nil
		}
	}
	err = sc.Err()
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return sandboxEnd{}, false, &sandboxError{msg: fmt.Sprintf("sandboxd stream: %v", err)}
}

// handle runs one message as one turn of the sandbox's conversation.
func (a *sandboxAgent) handle(m store.Message) {
	n := a.n
	if err := n.st.Extend(n.ctx, m.ID, m.LeaseID, a.cfg.Lease.D()); err != nil {
		return
	}
	tl := n.watchTurn(m, a.cfg.Lease.D())
	defer tl.stop()
	ctx := tl.ctx
	complete := func(state store.State, reply string, f Failure) {
		if err := n.st.Complete(n.ctx, m.ID, m.LeaseID, state, capReply(reply, int(n.cfg.Limits.MaxReply)), f.Code, f.Message); err != nil && !errors.Is(err, store.ErrLeaseLost) {
			n.log.Warn("complete", "id", m.ID, "err", err)
		}
		n.notify()
	}
	release := func(f Failure) {
		if _, err := n.st.Release(n.ctx, m.ID, m.LeaseID, f.Code, f.Message, n.cfg.Limits.MaxAttempts); err != nil && !errors.Is(err, store.ErrLeaseLost) {
			n.log.Warn("release", "id", m.ID, "err", err)
		}
		n.notify()
	}
	failed := func(err error, what string) {
		if n.ctx.Err() != nil || tl.cancelled.Load() || tl.lost.Load() {
			return
		}
		var se *sandboxError
		if errors.As(err, &se) && !se.retryable() {
			code := "agent_error"
			if se.status == http.StatusUnauthorized || se.status == http.StatusForbidden {
				code = "refused"
			}
			complete(store.Failed, "", failf(code, "%s: %v", what, err))
			return
		}
		n.log.Warn("sandbox", "agent", a.name, "err", err)
		release(failf("agent_unavailable", "%s: %v", what, err))
		time.Sleep(time.Second)
	}
	if a.cfg.Sandbox.Token == "" {
		complete(store.Failed, "", failf("refused", "%s is not set in the node's environment: sandboxd's API token", a.cfg.Sandbox.TokenEnv))
		return
	}
	if err := a.catchUp(ctx); err != nil {
		failed(err, "the sandbox's conversation")
		return
	}
	from := a.cursor.Load()
	if m.Attempts > 1 {
		// An earlier attempt may have posted it already (the same message there): its turn may be
		// anywhere in the stream.
		from = 0
	}
	run, delivery, posted, err := a.post(ctx, m)
	if err != nil {
		failed(err, "the message to the sandbox")
		return
	}
	had, _ := n.st.EventCount(n.ctx, m.ID)
	t := &sandboxTurn{a: a, msgID: m.ID, run: run, limit: int(n.cfg.Limits.MaxReply), after: from, mark: markOf(posted),
		// Joined to a run not started yet: nothing of the run comes before this message.
		started: delivery == "input" && run.Status == "queued"}
	t.stream = &textStream{record: func(kind string, data any) {
		if err := n.addEvent(n.ctx, m.ID, kind, eventJSON(data), true); err != nil && n.ctx.Err() == nil {
			n.log.Debug("event", "err", err)
		}
	}, budget: max(n.cfg.Limits.MaxEventsPerMessage-had, 0) / 2}
	t.stream.event("notice", map[string]string{"text": "sandbox " + a.cfg.Sandbox.ID + ": run " + run.ID + " (" + delivery + ")"})

	turnCtx, turnCancel := context.WithTimeout(ctx, a.cfg.TurnTimeout.D())
	end, err := a.follow(turnCtx, t)
	timedOut := errors.Is(turnCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	turnCancel()
	t.mu.Lock()
	for _, w := range t.asks {
		w.stop()
	}
	t.mu.Unlock()
	t.stream.close()
	a.recordUsage(m.ID, t)
	reply := t.reply(a.cfg.Reply)

	if err != nil || timedOut {
		if tl.cancelled.Load() || timedOut {
			// The run stops in the sandbox too.
			cctx, done := context.WithTimeout(n.ctx, 10*time.Second)
			_, _ = a.call(cctx, "POST", "/runs/"+url.PathEscape(run.ID)+"/cancel", nil, nil, nil)
			done()
		}
		switch {
		case n.ctx.Err() != nil || tl.lost.Load() || tl.cancelled.Load():
		case timedOut:
			complete(store.Failed, reply, failf("turn_timeout", "the turn ran past %s (turn_timeout); the sandbox's run was cancelled", a.cfg.TurnTimeout.D()))
		default:
			failed(err, "the sandbox's stream")
		}
		return
	}
	if end.state == store.Done {
		complete(store.Done, reply, Failure{})
		return
	}
	complete(store.Failed, reply, failf("agent_stopped", "the sandbox's agent stopped: %s", end.reason))
}

func (a *sandboxAgent) recordUsage(msgID string, t *sandboxTurn) {
	t.mu.Lock()
	u, used := t.usage, t.used
	t.mu.Unlock()
	if !used {
		return
	}
	n := a.n
	u.Turn, u.At, u.Agent, u.Reported = store.NewID(), time.Now().UTC(), a.addr+"@"+n.Name(), true
	u.Session = "sandbox:" + a.cfg.Sandbox.ID
	if t.run.Session != "" {
		u.Session += "/" + t.run.Session
	}
	if u.Model == "" {
		u.Model = a.cfg.Model
	}
	u.Total = u.Input + u.Output + u.CacheRead + u.CacheWrite
	if err := n.addEvent(n.ctx, msgID, "usage", eventJSON(u), false); err != nil && n.ctx.Err() == nil {
		n.log.Debug("usage", "err", err)
	}
}

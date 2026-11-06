// Package api serves a node's HTTP API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

type server struct {
	n     *node.Node
	local bool // the Unix socket: a request without a token is the node's user
	max   int64
}

// Handler serves the API of n. local says the listener is the node's Unix socket.
func Handler(n *node.Node, local bool, maxRequest int64) http.Handler {
	s := &server{n: n, local: local, max: maxRequest}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.auth(s.health))
	mux.HandleFunc("POST /v1/messages", s.auth(s.send))
	mux.HandleFunc("GET /v1/messages", s.auth(s.list))
	mux.HandleFunc("GET /v1/messages/{id}", s.auth(s.get))
	mux.HandleFunc("GET /v1/messages/{id}/wait", s.auth(s.wait))
	mux.HandleFunc("GET /v1/messages/{id}/events", s.auth(s.events))
	mux.HandleFunc("POST /v1/messages/{id}/events", s.auth(s.relayEvents))
	mux.HandleFunc("GET /v1/messages/{id}/files", s.auth(s.files))
	mux.HandleFunc("POST /v1/attach", s.auth(s.attach))
	mux.HandleFunc("POST /v1/messages/{id}/cancel", s.auth(s.cancel))
	mux.HandleFunc("POST /v1/messages/{id}/reply", s.auth(s.reply))
	mux.HandleFunc("POST /v1/messages/{id}/lease", s.auth(s.extend))
	mux.HandleFunc("GET /v1/agents", s.auth(s.agents))
	mux.HandleFunc("POST /v1/agents/{name}/claim", s.auth(s.claim))
	mux.HandleFunc("GET /v1/agents/{name}/sessions", s.auth(s.sessions))
	mux.HandleFunc("POST /v1/nodes/{name}/announce", s.auth(s.announce))
	mux.HandleFunc("POST /v1/nodes/{name}/claim", s.auth(s.nodeClaim))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, &node.Error{Code: "not_found", Message: r.Method + " " + r.URL.Path + ": no such endpoint"})
	})
	return mux
}

type handler func(w http.ResponseWriter, r *http.Request, p node.Principal)

func (s *server) auth(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		var p node.Principal
		switch {
		case tok != "":
			var ok bool
			if p, ok = s.n.PrincipalForToken(tok); !ok {
				writeStatus(w, http.StatusUnauthorized, &node.Error{Code: "refused", Message: "unknown token"})
				return
			}
		case s.local:
			p = s.n.LocalUser()
		default:
			writeStatus(w, http.StatusUnauthorized, &node.Error{Code: "refused", Message: "a token is needed (Authorization: Bearer …)"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.max)
		h(w, r, p)
	}
}

var statusOf = map[string]int{
	"invalid": http.StatusBadRequest, "unsupported": http.StatusNotImplemented, "refused": http.StatusForbidden, "not_found": http.StatusNotFound,
	"conflict": http.StatusConflict, "timeout": http.StatusRequestTimeout, "unavailable": http.StatusServiceUnavailable,
}

func writeErr(w http.ResponseWriter, err error) {
	var e *node.Error
	if !errors.As(err, &e) {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			e = &node.Error{Code: "refused", Message: "the request is over the size limit"}
		} else {
			e = &node.Error{Code: "internal", Message: err.Error()}
		}
	}
	code, ok := statusOf[e.Code]
	switch {
	case ok:
	case strings.HasPrefix(e.Code, "agent_") || strings.HasPrefix(e.Code, "session_"):
		code = http.StatusBadGateway // the node is fine; the agent behind it is not
	default:
		code = http.StatusInternalServerError
	}
	writeStatus(w, code, e)
}

func writeStatus(w http.ResponseWriter, code int, e *node.Error) {
	writeJSON(w, code, map[string]any{"error": e})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		return &node.Error{Code: "invalid", Message: "bad request body: " + err.Error()}
	}
	return nil
}

func duration(r *http.Request, key string, def, max time.Duration) (time.Duration, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, &node.Error{Code: "invalid", Message: key + ": a duration like 30s"}
	}
	if max > 0 && d > max {
		d = max
	}
	return d, nil
}

func (s *server) health(w http.ResponseWriter, r *http.Request, p node.Principal) {
	writeJSON(w, 200, s.n.Status(p))
}

func (s *server) send(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.SendRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	sr := node.SendRequest{To: req.To, Body: req.Body, Task: req.Task, Thread: req.Thread, Meta: req.Meta, Session: req.Session,
		Files: req.Files, IdemKey: r.Header.Get("Idempotency-Key")}
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeErr(w, &node.Error{Code: "invalid", Message: "ttl: a duration like 1h"})
			return
		}
		sr.TTL = d
	}
	if req.ID != "" || req.From != "" || req.Parent != "" || req.Depth != 0 {
		if p.Kind != "node" {
			writeErr(w, &node.Error{Code: "refused", Message: "id, from, parent and depth are set by the node"})
			return
		}
		sr.ID, sr.From, sr.Parent, sr.Depth = req.ID, req.From, req.Parent, req.Depth
	}
	m, created, err := s.n.Send(r.Context(), p, sr)
	if err != nil {
		writeErr(w, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, m)
}

func (s *server) get(w http.ResponseWriter, r *http.Request, p node.Principal) {
	m, err := s.n.Get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *server) wait(w http.ResponseWriter, r *http.Request, p node.Principal) {
	d, err := duration(r, "timeout", 30*time.Second, client.MaxWait)
	if err != nil {
		writeErr(w, err)
		return
	}
	m, err := s.n.Wait(r.Context(), p, r.PathValue("id"), d)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *server) list(w http.ResponseWriter, r *http.Request, p node.Principal) {
	q := r.URL.Query()
	f := store.Filter{To: q.Get("to"), From: q.Get("from"), Task: q.Get("task"), Thread: q.Get("thread"),
		State: store.State(q.Get("state")), After: q.Get("after")}
	for _, addr := range []*string{&f.To, &f.From} {
		if *addr != "" {
			a, err := node.ParseAddress(*addr)
			if err != nil {
				writeErr(w, &node.Error{Code: "invalid", Message: err.Error()})
				return
			}
			*addr = a.String()
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, &node.Error{Code: "invalid", Message: "limit: a positive number"})
			return
		}
		f.Limit = n
	}
	items, next, err := s.n.List(r.Context(), p, f)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []store.Message{}
	}
	writeJSON(w, 200, client.Page{Items: items, Next: next})
}

func (s *server) events(w http.ResponseWriter, r *http.Request, p node.Principal) {
	if r.URL.Query().Get("follow") != "" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.follow(w, r, p)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := s.n.Events(r.Context(), p, r.PathValue("id"), after, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	if evs == nil {
		evs = []store.Event{}
	}
	writeJSON(w, 200, map[string]any{"items": evs})
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request, p node.Principal) {
	m, err := s.n.Cancel(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *server) reply(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.ReplyRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Lease == "" {
		writeErr(w, &node.Error{Code: "invalid", Message: "lease is required"})
		return
	}
	if err := s.n.ReplyCoded(r.Context(), p, r.PathValue("id"), req.Lease, req.Body, req.ErrorCode, req.Error, req.Files); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) relayEvents(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.RelayRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	relayed, err := s.n.RelayEvents(r.Context(), p, r.PathValue("id"), req.Lease, req.After, req.Events)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]int64{"relayed": relayed})
}

func (s *server) files(w http.ResponseWriter, r *http.Request, p node.Principal) {
	files, err := s.n.Files(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	switch side := r.URL.Query().Get("side"); side {
	case "":
	case "message", "reply":
		var picked []store.File
		for _, f := range files {
			if f.Reply == (side == "reply") {
				picked = append(picked, f)
			}
		}
		files = picked
	default:
		writeErr(w, &node.Error{Code: "invalid", Message: "side: message or reply"})
		return
	}
	if files == nil {
		files = []store.File{}
	}
	writeJSON(w, 200, map[string]any{"items": files})
}

func (s *server) attach(w http.ResponseWriter, r *http.Request, p node.Principal) {
	// Only an agent in a turn may attach, nobody else's upload is read; and the attempt is the one
	// running when the upload began.
	turn, err := s.n.AttachTurn(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req client.AttachRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	id, err := s.n.Attach(r.Context(), turn, req.Files)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *server) extend(w http.ResponseWriter, r *http.Request, p node.Principal) {
	d, err := duration(r, "for", 5*time.Minute, time.Hour)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.Extend(r.Context(), p, r.PathValue("id"), r.URL.Query().Get("lease"), d); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) agents(w http.ResponseWriter, r *http.Request, p node.Principal) {
	list := s.n.Agents(r.Context())
	if list == nil {
		list = []client.AgentInfo{}
	}
	writeJSON(w, 200, list)
}

func (s *server) sessions(w http.ResponseWriter, r *http.Request, p node.Principal) {
	list, next, err := s.n.Sessions(r.Context(), p, r.PathValue("name"), r.URL.Query().Get("cwd"), r.URL.Query().Get("cursor"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list, "next": next})
}

func (s *server) claimParams(w http.ResponseWriter, r *http.Request) (wait, lease time.Duration, ok bool) {
	wait, err := duration(r, "wait", 0, client.MaxWait)
	if err == nil {
		lease, err = duration(r, "lease", 0, 24*time.Hour)
	}
	if err != nil {
		writeErr(w, err)
		return 0, 0, false
	}
	return wait, lease, true
}

func (s *server) claim(w http.ResponseWriter, r *http.Request, p node.Principal) {
	wait, lease, ok := s.claimParams(w, r)
	if !ok {
		return
	}
	m, got, err := s.n.Claim(r.Context(), p, r.PathValue("name"), wait, lease)
	switch {
	case err != nil:
		writeErr(w, err)
	case !got:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, 200, m)
	}
}

func (s *server) announce(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var a client.Announce
	if err := decode(r, &a); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.Announce(r.Context(), p, r.PathValue("name"), a.Agents); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) nodeClaim(w http.ResponseWriter, r *http.Request, p node.Principal) {
	wait, lease, ok := s.claimParams(w, r)
	if !ok {
		return
	}
	if lease == 0 {
		lease = 2 * time.Minute
	}
	m, got, err := s.n.NodeClaim(r.Context(), p, r.PathValue("name"), wait, lease)
	switch {
	case err != nil:
		writeErr(w, err)
	case !got:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, 200, m)
	}
}

// follow streams a message's events as Server-Sent Events while they are recorded: one `event:
// <kind>` per event (its seq as the SSE id, so Last-Event-ID resumes), then `event: end` with the
// final message. A comment every 15 s keeps idle connections open.
func (s *server) follow(w http.ResponseWriter, r *http.Request, p node.Principal) {
	id := r.PathValue("id")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if last, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil {
		after = last
	}
	if _, err := s.n.Get(r.Context(), p, id); err != nil {
		writeErr(w, err) // before the stream starts: a plain error
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, &node.Error{Code: "internal", Message: "streaming is not supported here"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	var mu sync.Mutex
	closed := false
	send := func(format string, args ...any) error {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return context.Canceled
		}
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}
	ctx, cancel := context.WithCancel(r.Context())
	var beat sync.WaitGroup
	defer func() { // nothing writes to w once the handler returns
		cancel()
		beat.Wait()
		mu.Lock()
		closed = true
		mu.Unlock()
	}()
	beat.Add(1)
	go func() {
		defer beat.Done()
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if send(": ping\n\n") != nil {
					cancel()
					return
				}
			}
		}
	}()
	m, err := s.n.Follow(ctx, p, id, after, func(e store.Event) error {
		b, _ := json.Marshal(e)
		return send("id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Kind, b)
	})
	if err != nil {
		return // the client went away, or the message is gone
	}
	b, _ := json.Marshal(m)
	_ = send("event: end\ndata: %s\n\n", b)
}

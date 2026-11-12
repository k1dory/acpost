// Package api serves a node's HTTP API.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/forge"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
	"github.com/k1dory/acpx"
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
	mux.HandleFunc("GET /v1/messages/by-idem", s.auth(s.byIdem))
	mux.HandleFunc("GET /v1/messages/{id}/wait", s.auth(s.wait))
	mux.HandleFunc("GET /v1/messages/{id}/events", s.auth(s.events))
	mux.HandleFunc("POST /v1/messages/{id}/events", s.auth(s.relayEvents))
	mux.HandleFunc("GET /v1/usage", s.auth(s.usage))
	mux.HandleFunc("GET /v1/traces", s.auth(s.traces))
	mux.HandleFunc("GET /v1/messages/{id}/changes", s.auth(s.changes))
	mux.HandleFunc("GET /v1/conversations", s.auth(s.conversations))
	mux.HandleFunc("POST /v1/messages/{id}/permission", s.auth(s.answer))
	mux.HandleFunc("GET /v1/permissions", s.auth(s.asking))
	mux.HandleFunc("GET /v1/forge", s.auth(s.forgeInfo))
	mux.HandleFunc("GET /v1/forge/pulls", s.auth(s.forgePulls))
	mux.HandleFunc("POST /v1/forge/pulls", s.auth(s.forgeOpenPull))
	mux.HandleFunc("POST /v1/forge/merge", s.auth(s.forgeMerge))
	mux.HandleFunc("GET /v1/forge/checks", s.auth(s.forgeChecks))
	mux.HandleFunc("GET /v1/forge/log", s.auth(s.forgeLog))
	mux.HandleFunc("POST /v1/terminals", s.auth(s.termOpen))
	mux.HandleFunc("GET /v1/terminals", s.auth(s.termList))
	mux.HandleFunc("GET /v1/terminals/{id}/output", s.auth(s.termOutput))
	mux.HandleFunc("POST /v1/terminals/{id}/input", s.auth(s.termInput))
	mux.HandleFunc("POST /v1/terminals/{id}/resize", s.auth(s.termResize))
	mux.HandleFunc("DELETE /v1/terminals/{id}", s.auth(s.termClose))
	mux.HandleFunc("GET /v1/messages/{id}/files", s.auth(s.files))
	mux.HandleFunc("POST /v1/attach", s.auth(s.attach))
	mux.HandleFunc("POST /v1/messages/{id}/cancel", s.auth(s.cancel))
	mux.HandleFunc("POST /v1/messages/{id}/disown", s.auth(s.disown)) // a joined node gives up a send it has no answer for
	mux.HandleFunc("PATCH /v1/messages/{id}", s.auth(s.editQueued))
	mux.HandleFunc("POST /v1/messages/{id}/reply", s.auth(s.reply))
	mux.HandleFunc("POST /v1/messages/{id}/lease", s.auth(s.extend))
	mux.HandleFunc("GET /v1/agents", s.auth(s.agents))
	mux.HandleFunc("POST /v1/agents/{name}/claim", s.auth(s.claim))
	mux.HandleFunc("GET /v1/agents/{name}/sessions", s.auth(s.sessions))
	mux.HandleFunc("GET /v1/agents/{name}/options", s.auth(s.options))
	mux.HandleFunc("GET /v1/agents/{name}/sessions/{id}/history", s.auth(s.sessionHistory))
	mux.HandleFunc("POST /v1/agents/{name}/sessions/{id}/fork", s.auth(s.forkSession))
	mux.HandleFunc("GET /v1/chats", s.auth(s.chats))
	mux.HandleFunc("GET /v1/forge/accounts", s.auth(s.forgeAccounts))
	mux.HandleFunc("POST /v1/forge/accounts", s.auth(s.forgeLogin))
	mux.HandleFunc("DELETE /v1/forge/accounts/{host}", s.auth(s.forgeLogout))
	mux.HandleFunc("POST /v1/handoff", s.auth(s.handoff))
	mux.HandleFunc("GET /v1/backlog", s.auth(s.backlog))
	mux.HandleFunc("PUT /v1/backlog", s.auth(s.putBacklog))
	mux.HandleFunc("DELETE /v1/backlog/{id}", s.auth(s.deleteBacklog))
	mux.HandleFunc("POST /v1/backlog/{id}/start", s.auth(s.startBacklog))
	mux.HandleFunc("GET /v1/messages/{id}/tree", s.auth(s.tree))
	mux.HandleFunc("GET /v1/file-at", s.auth(s.fileAt))
	mux.HandleFunc("GET /v1/wiki", s.auth(s.wikis))
	mux.HandleFunc("GET /v1/wiki/pages", s.auth(s.wikiPages))
	mux.HandleFunc("POST /v1/wiki", s.auth(s.writeWiki))
	mux.HandleFunc("DELETE /v1/wiki", s.auth(s.deleteWiki))
	mux.HandleFunc("GET /v1/knowledge", s.auth(s.knowledge))
	mux.HandleFunc("PUT /v1/knowledge", s.auth(s.putNote))
	mux.HandleFunc("DELETE /v1/knowledge/{name}", s.auth(s.deleteNote))
	mux.HandleFunc("GET /v1/schedules", s.auth(s.schedules))
	mux.HandleFunc("PUT /v1/schedules", s.auth(s.putSchedule))
	mux.HandleFunc("DELETE /v1/schedules/{id}", s.auth(s.deleteSchedule))
	mux.HandleFunc("POST /v1/schedules/{id}/run", s.auth(s.runSchedule))
	mux.HandleFunc("GET /v1/playbooks", s.auth(s.playbooks))
	mux.HandleFunc("PUT /v1/playbooks/{name}", s.auth(s.putPlaybook))
	mux.HandleFunc("DELETE /v1/playbooks/{name}", s.auth(s.deletePlaybook))
	mux.HandleFunc("PUT /v1/chats/title", s.auth(s.chatTitle))
	mux.HandleFunc("GET /v1/mcp", s.auth(s.mcpList))
	mux.HandleFunc("PUT /v1/mcp/{name}", s.auth(s.mcpPut))
	mux.HandleFunc("DELETE /v1/mcp/{name}", s.auth(s.mcpDelete))
	mux.HandleFunc("GET /v1/cloud/agents", s.auth(s.cloudAgents))
	mux.HandleFunc("POST /v1/cloud/tasks", s.auth(s.cloudTask))
	mux.HandleFunc("GET /v1/cloud/sandboxes/{id}/history", s.auth(s.cloudHistory))
	mux.HandleFunc("GET /v1/cloud", s.auth(s.cloudInfo))
	mux.HandleFunc("GET /v1/cloud/catalogue", s.auth(s.cloudCatalogue))
	mux.HandleFunc("POST /v1/cloud/agents/plan", s.auth(s.cloudPlanAgent))
	mux.HandleFunc("PUT /v1/cloud/agents/{name}", s.auth(s.cloudDeployAgent))
	mux.HandleFunc("DELETE /v1/cloud/agents/{name}", s.auth(s.cloudDeleteAgent))
	mux.HandleFunc("POST /v1/cloud/drafts", s.auth(s.cloudDraft))
	mux.HandleFunc("GET /v1/cloud/drafts/{id}", s.auth(s.cloudDraftResult))
	mux.HandleFunc("GET /v1/folders", s.auth(s.folders))
	mux.HandleFunc("POST /v1/folders", s.auth(s.createFolder))
	mux.HandleFunc("PATCH /v1/folders/{id}", s.auth(s.updateFolder))
	mux.HandleFunc("DELETE /v1/folders/{id}", s.auth(s.deleteFolder))
	mux.HandleFunc("PUT /v1/chats/folder", s.auth(s.moveChat))
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
		if p.Kind == "node" {
			r = r.WithContext(context.WithValue(r.Context(), lenientKey{}, true))
		}
		h(&cutWriter{ResponseWriter: w, ctx: r.Context()}, r, p)
	}
}

var statusOf = map[string]int{
	"invalid": http.StatusBadRequest, "unsupported": http.StatusNotImplemented, "refused": http.StatusForbidden, "not_found": http.StatusNotFound,
	"conflict": http.StatusConflict, "timeout": http.StatusRequestTimeout, "unavailable": http.StatusServiceUnavailable,
}

func writeErr(w http.ResponseWriter, err error) {
	if requestCut(w) {
		err = context.Canceled
	}
	code, e := errorOf(err)
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
	lenient := r.Context().Value(lenientKey{}) != nil
	if !lenient {
		dec.DisallowUnknownFields()
	}
	var err error
	if lenient {
		err = decodeLenient(dec, v)
	} else {
		err = dec.Decode(v)
	}
	if err == nil {
		// One value is the body: a second one or garbage after it is a broken client, not ignored.
		if _, err = dec.Token(); err == io.EOF {
			return nil
		} else if err == nil {
			err = errors.New("more than one JSON value")
		}
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return err
	}
	return &node.Error{Code: "invalid", Message: "bad request body: " + err.Error()}
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
		Cwd: req.Cwd, Model: req.Model, Effort: req.Effort, Mode: req.Mode,
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
	after, err := intParam(r, "after")
	if err != nil {
		writeErr(w, err)
		return
	}
	limit, err := intParam(r, "limit")
	if err != nil {
		writeErr(w, err)
		return
	}
	evs, err := s.n.Events(r.Context(), p, r.PathValue("id"), after, int(limit))
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

func (s *server) editQueued(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		Body string `json:"body"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	m, err := s.n.EditQueued(r.Context(), p, r.PathValue("id"), req.Body)
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

func (s *server) changes(w http.ResponseWriter, r *http.Request, p node.Principal) {
	q := r.URL.Query()
	turns, whole, err := s.n.Changes(r.Context(), p, r.PathValue("id"), q.Get("tree") != "", q.Get("diff") != "")
	if err != nil {
		writeErr(w, err)
		return
	}
	if turns == nil {
		turns = []store.TurnChanges{}
	}
	writeJSON(w, 200, client.Changes{Turns: turns, Repos: whole})
}

func (s *server) usage(w http.ResponseWriter, r *http.Request, p node.Principal) {
	q := store.UsageQuery{By: r.URL.Query().Get("by"), Agent: r.URL.Query().Get("agent")}
	for _, t := range []struct {
		name string
		into *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if v := r.URL.Query().Get(t.name); v != "" {
			at, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeErr(w, &node.Error{Code: "invalid", Message: t.name + ": an RFC 3339 time"})
				return
			}
			*t.into = at
		}
	}
	items, err := s.n.Usage(r.Context(), p, q)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *server) traces(w http.ResponseWriter, r *http.Request, p node.Principal) {
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, &node.Error{Code: "invalid", Message: "limit: a number from 1"})
			return
		}
		limit = n
	}
	out, err := s.n.Traces(r.Context(), p, q.Get("thread"), limit)
	items(w, out, err)
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

func (s *server) options(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.AgentOptions(r.Context(), p, r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *server) sessionHistory(w http.ResponseWriter, r *http.Request, p node.Principal) {
	evs, err := s.n.SessionHistory(r.Context(), p, r.PathValue("name"), r.PathValue("id"), r.URL.Query().Get("cwd"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": evs})
}

func (s *server) forkSession(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		Cwd string `json:"cwd"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
	}
	id, err := s.n.ForkSession(r.Context(), p, r.PathValue("name"), r.PathValue("id"), req.Cwd)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"session": id})
}

func (s *server) chats(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Chats(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *server) cloudAgents(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.CloudAgents(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": out, "configured": s.n.CloudConfigured()})
}

func (s *server) cloudTask(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.CloudTask
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.n.CreateCloudTask(r.Context(), p, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *server) cloudHistory(w http.ResponseWriter, r *http.Request, p node.Principal) {
	evs, err := s.n.CloudHistory(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": evs})
}

func (s *server) forgeAccounts(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.ForgeAccounts(r.Context(), p)
	items(w, out, err)
}

func (s *server) forgeLogin(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct{ Host, Kind, Token string }
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.n.ForgeLogin(r.Context(), p, req.Host, req.Kind, req.Token)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *server) forgeLogout(w http.ResponseWriter, r *http.Request, p node.Principal) {
	noContent(w, s.n.ForgeLogout(r.Context(), p, r.PathValue("host")))
}

func (s *server) mcpList(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.MCPServers(r.Context(), p)
	items(w, out, err)
}

func (s *server) mcpPut(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.MCPServer
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	req.Name = r.PathValue("name")
	if err := s.n.PutMCPServer(r.Context(), p, req.MCPServer, req.Secrets); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *server) mcpDelete(w http.ResponseWriter, r *http.Request, p node.Principal) {
	if err := s.n.DeleteMCPServer(r.Context(), p, r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *server) backlog(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Backlog(r.Context(), p)
	items(w, out, err)
}

func (s *server) putBacklog(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req store.BacklogItem
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.n.PutBacklogItem(r.Context(), p, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *server) deleteBacklog(w http.ResponseWriter, r *http.Request, p node.Principal) {
	noContent(w, s.n.DeleteBacklogItem(r.Context(), p, r.PathValue("id")))
}

func (s *server) startBacklog(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		Agent string `json:"agent"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
	}
	m, err := s.n.StartBacklogItem(r.Context(), p, r.PathValue("id"), req.Agent)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, m)
}

func (s *server) tree(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Tree(r.Context(), p, r.PathValue("id"))
	items(w, out, err)
}

func (s *server) fileAt(w http.ResponseWriter, r *http.Request, p node.Principal) {
	q := r.URL.Query()
	text, err := s.n.FileAt(r.Context(), p, q.Get("repo"), q.Get("tree"), q.Get("path"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}

func (s *server) handoff(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req node.HandoffRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.n.Handoff(r.Context(), p, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *server) wikis(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Wikis(r.Context(), p)
	items(w, out, err)
}

func (s *server) wikiPages(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.WikiPages(r.Context(), p, r.URL.Query().Get("repo"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

func (s *server) writeWiki(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		Repo  string `json:"repo"`
		Agent string `json:"agent"`
		Lang  string `json:"lang"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	m, err := s.n.WriteWiki(r.Context(), p, req.Repo, req.Agent, req.Lang)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, m)
}

func (s *server) deleteWiki(w http.ResponseWriter, r *http.Request, p node.Principal) {
	noContent(w, s.n.DeleteWiki(r.Context(), p, r.URL.Query().Get("repo")))
}

func (s *server) knowledge(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Knowledge(r.Context(), p)
	items(w, out, err)
}

func (s *server) putNote(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req store.Note
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.PutNote(r.Context(), p, req); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *server) deleteNote(w http.ResponseWriter, r *http.Request, p node.Principal) {
	noContent(w, s.n.DeleteNote(r.Context(), p, r.PathValue("name")))
}

func (s *server) schedules(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Schedules(r.Context(), p)
	items(w, out, err)
}

func (s *server) putSchedule(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req store.Schedule
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.n.PutSchedule(r.Context(), p, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *server) deleteSchedule(w http.ResponseWriter, r *http.Request, p node.Principal) {
	noContent(w, s.n.DeleteSchedule(r.Context(), p, r.PathValue("id")))
}

func (s *server) runSchedule(w http.ResponseWriter, r *http.Request, p node.Principal) {
	m, err := s.n.RunSchedule(r.Context(), p, r.PathValue("id"))
	reply(w, http.StatusCreated, m, err)
}

func (s *server) playbooks(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Playbooks(r.Context(), p)
	items(w, out, err)
}

func (s *server) putPlaybook(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req store.Playbook
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	req.Name = r.PathValue("name")
	if err := s.n.PutPlaybook(r.Context(), p, req); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *server) deletePlaybook(w http.ResponseWriter, r *http.Request, p node.Principal) {
	noContent(w, s.n.DeletePlaybook(r.Context(), p, r.PathValue("name")))
}

func (s *server) chatTitle(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct{ Chat, Title string }
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.SetChatTitle(r.Context(), p, req.Chat, req.Title); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *server) folders(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.Folders(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

func (s *server) createFolder(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	f, err := s.n.CreateFolder(r.Context(), p, req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, f)
}

func (s *server) updateFolder(w http.ResponseWriter, r *http.Request, p node.Principal) {
	req := struct {
		Name string `json:"name"`
		Pos  *int   `json:"pos"`
	}{}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	pos := -1
	if req.Pos != nil {
		pos = max(*req.Pos, 0)
	}
	if err := s.n.UpdateFolder(r.Context(), p, r.PathValue("id"), req.Name, pos); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *server) deleteFolder(w http.ResponseWriter, r *http.Request, p node.Principal) {
	if err := s.n.DeleteFolder(r.Context(), p, r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *server) moveChat(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		Chat   string `json:"chat"`
		Folder string `json:"folder"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.MoveChat(r.Context(), p, req.Chat, req.Folder); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
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
	after, err := intParam(r, "after")
	if err != nil {
		writeErr(w, err)
		return
	}
	if last, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil {
		after = last
	}
	if _, err := s.n.Get(r.Context(), p, id); err != nil {
		writeErr(w, err) // before the stream starts: a plain error
		return
	}
	st, ok := newSSE(w, r)
	if !ok {
		return
	}
	defer st.done()
	st.start() // at once: the client sees the stream open before the first event
	m, err := s.n.Follow(st.ctx, p, id, after, func(e store.Event) error {
		b, _ := json.Marshal(e)
		return st.send("id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Kind, b)
	})
	if err != nil {
		st.fail(err)
		return
	}
	b, _ := json.Marshal(m)
	_ = st.send("event: end\ndata: %s\n\n", b)
}

func (s *server) termOpen(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req node.TermRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	t, err := s.n.OpenTerminal(r.Context(), p, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, t)
}

func (s *server) termList(w http.ResponseWriter, r *http.Request, p node.Principal) {
	ts, err := s.n.Terminals(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": ts})
}

func (s *server) termInput(w http.ResponseWriter, r *http.Request, p node.Principal) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil {
		writeErr(w, &node.Error{Code: "invalid", Message: err.Error()})
		return
	}
	if err := s.n.TermInput(p, r.PathValue("id"), b); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) termResize(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct{ Cols, Rows int }
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.TermResize(p, r.PathValue("id"), req.Cols, req.Rows); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) termClose(w http.ResponseWriter, r *http.Request, p node.Principal) {
	if err := s.n.CloseTerminal(p, r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// termOutput streams a terminal's output as Server-Sent Events: `event: data` with the offset as
// its id and the bytes in base64, then `event: end` with the terminal when its shell has ended.
// ?from= (or Last-Event-ID, the offset after the last bytes read) goes on from there.
func (s *server) termOutput(w http.ResponseWriter, r *http.Request, p node.Principal) {
	from, err := intParam(r, "from")
	if err != nil {
		writeErr(w, err)
		return
	}
	if last, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil {
		from = last
	}
	// The stream starts with the first output: a terminal that is not there, or not this
	// principal's, is a plain error from the node's own check.
	st, ok := newSSE(w, r)
	if !ok {
		return
	}
	defer st.done()
	t, err := s.n.TermOutput(st.ctx, p, r.PathValue("id"), from, func(off int64, b []byte) error {
		return st.send("id: %d\nevent: data\ndata: %s\n\n", off+int64(len(b)), base64.StdEncoding.EncodeToString(b))
	})
	if err != nil {
		st.fail(err)
		return
	}
	b, _ := json.Marshal(t)
	_ = st.send("event: end\ndata: %s\n\n", b)
}

func forgeQuery(r *http.Request) node.ForgeQuery {
	return node.ForgeQuery{Agent: r.URL.Query().Get("agent"), Dir: r.URL.Query().Get("dir")}
}

func (s *server) forgeInfo(w http.ResponseWriter, r *http.Request, p node.Principal) {
	info, err := s.n.Forge(r.Context(), p, forgeQuery(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, info)
}

func (s *server) forgePulls(w http.ResponseWriter, r *http.Request, p node.Principal) {
	l, err := s.n.PullList(r.Context(), p, forgeQuery(r), r.URL.Query().Get("state"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": nonNilSlice(l.Items), "truncated": l.Truncated})
}

func (s *server) forgeChecks(w http.ResponseWriter, r *http.Request, p node.Principal) {
	l, err := s.n.CheckList(r.Context(), p, forgeQuery(r), r.URL.Query().Get("ref"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": nonNilSlice(l.Items), "truncated": l.Truncated})
}

func (s *server) forgeLog(w http.ResponseWriter, r *http.Request, p node.Principal) {
	q := r.URL.Query()
	text, err := s.n.CILog(r.Context(), p, forgeQuery(r), q.Get("job"), q.Get("target"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, text)
}

func (s *server) forgeOpenPull(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		node.ForgeQuery
		forge.PullRequest
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	pull, err := s.n.OpenPull(r.Context(), p, req.ForgeQuery, req.PullRequest)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, pull)
}

func (s *server) forgeMerge(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req struct {
		node.ForgeQuery
		forge.Merge
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.MergePull(r.Context(), p, req.ForgeQuery, req.Merge); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"merged": true, "number": req.Number})
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (s *server) conversations(w http.ResponseWriter, r *http.Request, p node.Principal) {
	limit, err := intParam(r, "limit")
	if err != nil {
		writeErr(w, err)
		return
	}
	items, next, err := s.n.Conversations(r.Context(), p, r.URL.Query().Get("before"), int(limit))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": nonNilSlice(items), "next": next})
}

func (s *server) answer(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var reply acpx.Reply
	if err := decode(r, &reply); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.n.Answer(r.Context(), p, r.PathValue("id"), reply); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) asking(w http.ResponseWriter, r *http.Request, p node.Principal) {
	items, err := s.n.Asking(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// lenientKey marks a request from another node: its body may carry fields this version does not
// know (a newer node or hub), which are ignored rather than refused, so versions can differ across
// a hub. Users, agents and clients talk to their own node's version and are held to it.
type lenientKey struct{}

// intParam is the query parameter key as a number ≥ 0, 0 when absent: garbage is invalid rather
// than read as 0 (a stream from the start, a default page).
func intParam(r *http.Request, key string) (int64, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, &node.Error{Code: "invalid", Message: key + ": a whole number ≥ 0"}
	}
	return n, nil
}

// errorOf is the API error err is, with its HTTP status.
func errorOf(err error) (int, *node.Error) {
	var e *node.Error
	if !errors.As(err, &e) {
		var mbe *http.MaxBytesError
		if errors.Is(err, context.Canceled) {
			// The request was cut short: its client went away, or the node stops (it ends every
			// request then). Worth asking again, at this node or the next, not an internal error.
			e = &node.Error{Code: "unavailable", Message: "the request ended before its answer: the node may be stopping"}
		} else if errors.As(err, &mbe) {
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
	return code, e
}

// sseStream writes one Server-Sent Events response. It starts (headers, 200) with its first
// write, so an error before anything was sent is still a plain JSON error; after the start an
// error is an `event: acpost-error` with the same envelope (and the status), which the client ends on
// instead of reconnecting to a stream that fails the same way. A comment every ssePing keeps an
// idle connection open through proxies; once done returns nothing writes to w.
type sseStream struct {
	w      http.ResponseWriter
	fl     http.Flusher
	ctx    context.Context // the request's; ends when a heartbeat cannot be written
	cancel context.CancelFunc
	beat   sync.WaitGroup

	mu      sync.Mutex
	started bool
	closed  bool
}

var ssePing = 15 * time.Second

func newSSE(w http.ResponseWriter, r *http.Request) (*sseStream, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, &node.Error{Code: "internal", Message: "streaming is not supported here"})
		return nil, false
	}
	ctx, cancel := context.WithCancel(r.Context())
	st := &sseStream{w: w, fl: fl, ctx: ctx, cancel: cancel}
	st.beat.Add(1)
	go st.ping()
	return st, true
}

func (st *sseStream) ping() {
	defer st.beat.Done()
	t := time.NewTicker(ssePing)
	defer t.Stop()
	for {
		select {
		case <-st.ctx.Done():
			return
		case <-t.C:
			if st.send(": ping\n\n") != nil {
				st.cancel()
				return
			}
		}
	}
}

// start sends the headers if nothing has been sent yet.
func (st *sseStream) start() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.closed {
		st.startLocked()
	}
}

func (st *sseStream) startLocked() {
	if st.started {
		return
	}
	st.started = true
	h := st.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	st.w.WriteHeader(http.StatusOK)
	st.fl.Flush()
}

func (st *sseStream) send(format string, args ...any) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return context.Canceled
	}
	st.startLocked()
	if _, err := fmt.Fprintf(st.w, format, args...); err != nil {
		return err
	}
	st.fl.Flush()
	return nil
}

// fail reports err to the client, unless the client is gone (or the node stops: its clients
// reconnect to the next one).
func (st *sseStream) fail(err error) {
	cut := st.ctx.Err() != nil
	if cut {
		err = context.Canceled // what the layers below made of it is not the answer
	}
	code, e := errorOf(err)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return
	}
	if !st.started {
		st.started = true
		st.closed = true // the answer is this JSON: no heartbeat after it
		writeStatus(st.w, code, e)
		return
	}
	if cut {
		return // the stream began: its client reconnects
	}
	b, _ := json.Marshal(map[string]any{"status": code, "error": e})
	if _, err := fmt.Fprintf(st.w, "event: "+sseErrorEvent+"\ndata: %s\n\n", b); err == nil {
		st.fl.Flush()
	}
}

func (st *sseStream) done() {
	st.cancel()
	st.beat.Wait()
	st.mu.Lock()
	st.closed = true
	st.mu.Unlock()
}

// The usual ends of a handler: the node's answer, or its error as the API's.

func reply[T any](w http.ResponseWriter, code int, v T, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, code, v)
}

// items answers {"items":[…]}: an empty list is [], never null.
func items[T any](w http.ResponseWriter, list []T, err error) {
	reply(w, http.StatusOK, map[string][]T{"items": nonNilSlice(list)}, err)
}

func noContent(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sseErrorEvent names the event a stream fails with. Event kinds are [a-z_] (an agent's, or relayed
// by a node), so a name with a dash is never one of them.
const sseErrorEvent = "acpost-error"

// decodeLenient decodes a body from another node into v, ignoring fields this version does not
// know. A list of files decodes strictly in the store (a misspelt field there is an error), so
// in it the fields a store.File does not have are dropped first. Nothing here grows with what
// the sender pads the body with: the object is walked a value at a time and only the files'
// place in it kept, and the list is walked a file at a time, at most MaxFilesPerRequest of them.
func decodeLenient(dec *json.Decoder, v any) error {
	var body json.RawMessage
	if err := dec.Decode(&body); err != nil {
		return err
	}
	type span struct {
		start, end int
		files      []byte
	}
	var spans []span
	walk := json.NewDecoder(bytes.NewReader(body))
	if t, err := walk.Token(); err != nil || t != json.Delim('{') {
		return json.Unmarshal(body, v) // not an object: v says what is wrong with it
	}
	var val json.RawMessage // one buffer for every value: what is skipped is not kept
	for walk.More() {
		t, err := walk.Token()
		if err != nil {
			return err
		}
		if err := walk.Decode(&val); err != nil {
			return err
		}
		if key, _ := t.(string); strings.EqualFold(key, "files") {
			if len(spans) > 0 {
				// One list of files: a key given twice is no body a node sends (and would
				// have each copy rebuild the body).
				return errors.New("files: given more than once")
			}
			files, err := knownFileFields(val)
			if err != nil {
				return err
			}
			end := int(walk.InputOffset())
			spans = append(spans, span{end - len(val), end, files})
		}
	}
	for _, sp := range spans { // at most one
		body = append(body[:sp.start:sp.start], append(sp.files, body[sp.end:]...)...)
	}
	return json.Unmarshal(body, v)
}

// knownFileFields is a list of files with only the fields a store.File has, read a file at a time.
func knownFileFields(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if t != json.Delim('[') {
		return bytes.Clone(raw), nil // null, or not a list: the store says what is wrong with it
	}
	var out []store.File
	for dec.More() {
		if len(out) == store.MaxFilesPerRequest {
			return nil, fmt.Errorf("files: more than %d", store.MaxFilesPerRequest)
		}
		// Decoded as a store.File itself, without the store's strictness: a field it does not
		// have is scanned past, not kept.
		var f store.File
		if err := dec.Decode(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return json.Marshal(out)
}

// cutWriter is a handler's ResponseWriter with its request's context. An error answered after the
// request ended (the node stops, or its client went away) is the API's unavailable, whatever the
// layers below made of the cancellation (a rolled-back transaction, an ACP request cancelled, a
// git killed): nothing a cut request reports is final. A success is still written as it is.
type cutWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w *cutWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *cutWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestCut(w http.ResponseWriter) bool {
	cw, ok := w.(*cutWriter)
	return ok && cw.ctx.Err() != nil
}

// byIdem: a joined node looks up what one of its senders sent here with an idempotency key. None
// is a 200 too, {"found":false}: a node tells it from the 404 of a hub without this route (an
// older version, where the path is read as a message id), which proves nothing.
func (s *server) byIdem(w http.ResponseWriter, r *http.Request, p node.Principal) {
	m, err := s.n.MessageByIdem(r.Context(), p, r.URL.Query().Get("from"), r.URL.Query().Get("key"))
	var e *node.Error
	if errors.As(err, &e) && e.Code == "not_found" {
		reply(w, http.StatusOK, map[string]any{"found": false}, nil)
		return
	}
	reply(w, http.StatusOK, map[string]any{"found": true, "message": m}, err)
}

// disown: a joined node gives up a message it sent and never had the answer for. The hub has it
// (found, with the message: the node follows it) or refuses it from now on (found false).
func (s *server) disown(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.DisownRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	m, found, err := s.n.Disown(r.Context(), p, r.PathValue("id"), req.From, req.Key)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": true, "message": m})
}

func (s *server) cloudInfo(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.CloudInfo(r.Context(), p)
	reply(w, http.StatusOK, out, err)
}

func (s *server) cloudCatalogue(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.CloudCatalogue(r.Context(), p)
	reply(w, http.StatusOK, out, err)
}

func (s *server) cloudPlanAgent(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.CloudAgentSpec
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.n.CloudPlanAgent(r.Context(), p, r.URL.Query().Get("cloud"), req)
	reply(w, http.StatusOK, out, err)
}

func (s *server) cloudDeployAgent(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.CloudAgentSpec
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Name == "" {
		req.Name = r.PathValue("name")
	}
	if req.Name != r.PathValue("name") {
		writeErr(w, &node.Error{Code: "invalid", Message: "the definition names another agent than the address"})
		return
	}
	out, err := s.n.CloudDeployAgent(r.Context(), p, r.URL.Query().Get("cloud"), req, r.URL.Query().Get("plan"))
	reply(w, http.StatusOK, out, err)
}

func (s *server) cloudDeleteAgent(w http.ResponseWriter, r *http.Request, p node.Principal) {
	if err := s.n.CloudDeleteAgent(r.Context(), p, r.URL.Query().Get("cloud"), r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) cloudDraft(w http.ResponseWriter, r *http.Request, p node.Principal) {
	var req client.CloudDraftRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.n.CloudDraft(r.Context(), p, req)
	reply(w, http.StatusCreated, out, err)
}

func (s *server) cloudDraftResult(w http.ResponseWriter, r *http.Request, p node.Principal) {
	out, err := s.n.CloudDraftResult(r.Context(), p, r.PathValue("id"))
	reply(w, http.StatusOK, out, err)
}

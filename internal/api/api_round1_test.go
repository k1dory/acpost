package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

const (
	nodeSecret   = "node-secret"
	clientSecret = "client-secret"
)

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// testServer is a node with a node token (peer) and a client token (ci), behind its API as the
// TCP listener sees it and as the Unix socket sees it.
func testServer(t *testing.T) (n *node.Node, tcp, local *httptest.Server) {
	t.Helper()
	y := fmt.Sprintf(`node: {name: hub, listen: ["tcp://127.0.0.1:0"]}
agents:
  bot: {driver: inbox}
tokens:
  - {name: peer, sha256: %s, node: true}
  - {name: ci, sha256: %s, send_to: ["agent:bot"]}
`, sha(nodeSecret), sha(clientSecret))
	cfg, err := config.Parse([]byte(y), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store = "memory:"
	st, err := store.Open(cfg.Store, node.StoreOptions(cfg))
	if err != nil {
		t.Fatal(err)
	}
	n, err = node.New(cfg, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	tcp = httptest.NewServer(Handler(n, false, 1<<20))
	local = httptest.NewServer(Handler(n, true, 1<<20))
	t.Cleanup(func() { tcp.Close(); local.Close(); n.Close(); st.Close() })
	return n, tcp, local
}

func call(t *testing.T, srv *httptest.Server, method, path, authz, body string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // a stream that never ends fails, not hangs
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var b strings.Builder
	_, _ = bufio.NewReader(resp.Body).WriteTo(&b)
	return resp.StatusCode, b.String()
}

func errCode(body string) string {
	var eb struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &eb)
	return eb.Error.Code
}

// A node of a newer version may send fields this one does not know: they are ignored. A client of
// this node's own is held to the schema, and one body is one JSON value for everybody.
func TestDecodeLenientForNodesStrictForOthers(t *testing.T) {
	_, tcp, _ := testServer(t)
	peer, ci := "Bearer "+nodeSecret, "Bearer "+clientSecret

	code, body := call(t, tcp, "POST", "/v1/messages", peer, `{"to":"agent:bot","from":"user:me@peer","body":"x","from_the_future":1}`)
	if code != http.StatusCreated {
		t.Fatalf("node with an unknown field: %d %s", code, body)
	}
	code, body = call(t, tcp, "POST", "/v1/messages", ci, `{"to":"agent:bot","body":"x","from_the_future":1}`)
	if code != http.StatusBadRequest || errCode(body) != "invalid" {
		t.Fatalf("client with an unknown field: %d %s", code, body)
	}
	for name, b := range map[string]string{
		"garbage after": `{"to":"agent:bot","body":"x"} xyz`,
		"two values":    `{"to":"agent:bot","body":"x"}{"to":"agent:bot","body":"y"}`,
	} {
		for who, authz := range map[string]string{"node": peer, "client": ci} {
			b := b
			if who == "node" {
				b = strings.Replace(b, `"body"`, `"from":"user:me@peer","body"`, 1)
			}
			code, body := call(t, tcp, "POST", "/v1/messages", authz, b)
			if code != http.StatusBadRequest || errCode(body) != "invalid" {
				t.Errorf("%s from a %s: %d %s", name, who, code, body)
			}
		}
	}
	if code, body := call(t, tcp, "POST", "/v1/messages", ci, "{\"to\":\"agent:bot\",\"body\":\"x\"}\n\n"); code != http.StatusCreated {
		t.Fatalf("white space after the value: %d %s", code, body)
	}
}

// A credential that is not a bearer token is refused, also on the Unix socket where no
// Authorization at all is the node's user. (CutPrefix leaves such a header whole, so it is looked
// up as a token and is unknown: a contract kept, not a fix.)
func TestAuthorizationOtherThanBearerIsRefused(t *testing.T) {
	_, tcp, local := testServer(t)
	for _, authz := range []string{"Basic dXNlcjpwdw==", "Bearer", "Bearer ", "bearer " + clientSecret, "Token " + clientSecret} {
		for name, srv := range map[string]*httptest.Server{"socket": local, "tcp": tcp} {
			if code, body := call(t, srv, "GET", "/v1/health", authz, ""); code != http.StatusUnauthorized || errCode(body) != "refused" {
				t.Errorf("%s, Authorization %q: %d %s", name, authz, code, body)
			}
		}
	}
	if code, _ := call(t, local, "GET", "/v1/health", "", ""); code != 200 {
		t.Fatalf("socket without Authorization: %d", code)
	}
	if code, _ := call(t, tcp, "GET", "/v1/health", "Bearer "+clientSecret, ""); code != 200 {
		t.Fatalf("tcp with a token: %d", code)
	}
}

// A number in the query that is not one is invalid, not 0 (a stream from the start, a default page).
func TestIntegerQueryParamsAreChecked(t *testing.T) {
	_, _, local := testServer(t)
	code, body := call(t, local, "POST", "/v1/messages", "", `{"to":"agent:bot","body":"x"}`)
	if code != http.StatusCreated {
		t.Fatalf("send: %d %s", code, body)
	}
	var m store.Message
	_ = json.Unmarshal([]byte(body), &m)
	for _, path := range []string{
		"/v1/messages/" + m.ID + "/events?after=abc",
		"/v1/messages/" + m.ID + "/events?limit=ten",
		"/v1/messages/" + m.ID + "/events?follow=1&after=abc",
		"/v1/messages/" + m.ID + "/events?after=-1",
		"/v1/conversations?limit=x",
		"/v1/terminals/term_x/output?from=x",
	} {
		if code, body := call(t, local, "GET", path, "", ""); code != http.StatusBadRequest || errCode(body) != "invalid" {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	if code, body := call(t, local, "GET", "/v1/messages/"+m.ID+"/events?after=0&limit=5", "", ""); code != 200 {
		t.Fatalf("numbers that are numbers: %d %s", code, body)
	}
}

// A terminal's output stream keeps an idle connection open with heartbeats, as follow does; a
// terminal that is not there is a plain JSON 404, not an empty event stream.
func TestTermOutputHeartbeatAndPlainErrors(t *testing.T) {
	old := ssePing
	ssePing = 100 * time.Millisecond
	t.Cleanup(func() { ssePing = old })
	_, tcp, local := testServer(t)

	code, body := call(t, local, "GET", "/v1/terminals/term_nope/output", "", "")
	if code != 404 || errCode(body) != "not_found" {
		t.Fatalf("no such terminal: %d %s", code, body)
	}
	if code, body := call(t, tcp, "GET", "/v1/terminals/term_nope/output", "Bearer "+clientSecret, ""); code != 403 || errCode(body) != "refused" {
		t.Fatalf("a token's terminal output: %d %s", code, body)
	}

	code, body = call(t, local, "POST", "/v1/terminals", "", fmt.Sprintf(`{"cwd":%q}`, t.TempDir()))
	if code != 201 {
		t.Fatalf("open: %d %s", code, body)
	}
	var tm node.Terminal
	_ = json.Unmarshal([]byte(body), &tm)
	t.Cleanup(func() { call(t, local, "DELETE", "/v1/terminals/"+tm.ID, "", "") })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", local.URL+"/v1/terminals/"+tm.ID+"/output", nil)
	resp, err := local.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if sc.Text() == ": ping" {
			return
		}
	}
	t.Fatalf("no heartbeat on the terminal's stream: %v", sc.Err())
}

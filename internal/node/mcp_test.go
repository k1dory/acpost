package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// The MCP servers of Settings reach every session an agent opens, their secrets from the keychain
// (a file in tests); a server for other agents, or turned off, does not.
func TestMCPServers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ACPOST_SECRETS_FILE", "1")
	n := acpNode(t, "")
	err := n.PutMCPServer(ctx, me, store.MCPServer{Name: "wiki", Kind: "stdio", Command: "wiki-mcp", Env: []string{"TOKEN"}, Enabled: true},
		map[string]string{"env/TOKEN": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	n.PutMCPServer(ctx, me, store.MCPServer{Name: "off", Kind: "stdio", Command: "x", Enabled: false}, nil)
	n.PutMCPServer(ctx, me, store.MCPServer{Name: "other", Kind: "stdio", Command: "y", Agents: []string{"someone"}, Enabled: true}, nil)
	if m := ask(t, n, SendRequest{Body: "mcp"}); m.Reply != "wiki:wiki-mcp:TOKEN=s3cret" {
		t.Fatalf("the session got %q", m.Reply)
	}
	list, _ := n.MCPServers(ctx, me)
	b, _ := json.Marshal(list)
	if len(list) != 3 || strings.Contains(string(b), "s3cret") {
		t.Fatalf("listed %s", b)
	}
	if err := n.PutMCPServer(ctx, Principal{Kind: "client", Name: "ci"}, store.MCPServer{Name: "x", Kind: "stdio", Command: "x"}, nil); code(err) != "refused" {
		t.Fatalf("a client added an MCP server: %v", err)
	}
	if err := n.PutMCPServer(ctx, me, store.MCPServer{Name: "bad name!", Kind: "stdio", Command: "x"}, nil); code(err) != "invalid" {
		t.Fatalf("a bad name: %v", err)
	}
	// A variable taken out: its value goes.
	if err := n.PutMCPServer(ctx, me, store.MCPServer{Name: "wiki", Kind: "stdio", Command: "wiki-mcp", Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	if n.mcpKeychain().Get("wiki/env/TOKEN") != "" {
		t.Fatal("a variable taken out kept its value")
	}
	if err := n.DeleteMCPServer(ctx, me, "wiki"); err != nil {
		t.Fatal(err)
	}
	if n.mcpKeychain().Get("wiki/env/TOKEN") != "" {
		t.Fatal("the secret outlived its server")
	}
}

// A forge account is kept once the forge says whose the token is; a token it refuses is not.
func TestForgeAccounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ACPOST_SECRETS_FILE", "1")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			w.Write([]byte(`{"version":"11.0.1"}`))
		case "/api/v1/user":
			if r.Header.Get("Authorization") != "token good" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"login":"lev"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	n := newNode(t, inboxCfg)
	c, _ := n.forgeClient()
	c.HTTP = srv.Client()
	if _, err := n.ForgeLogin(ctx, me, host, "", "bad"); code(err) != "refused" {
		t.Fatalf("a refused token: %v", err)
	}
	acc, err := n.ForgeLogin(ctx, me, host, "", "good")
	// A refused token does not replace the one kept.
	if _, err := n.ForgeLogin(ctx, me, host, "", "bad"); code(err) != "refused" {
		t.Fatalf("a refused token: %v", err)
	}
	if list, _ := n.ForgeAccounts(ctx, me); len(list) != 1 || list[0].Login != "lev" {
		t.Fatalf("the good token went: %+v", list)
	}
	if err != nil || acc.Login != "lev" || acc.Kind != "forgejo" || acc.Version != "11.0.1" {
		t.Fatalf("%+v %v", acc, err)
	}
	list, _ := n.ForgeAccounts(ctx, me)
	if len(list) != 1 || list[0].Login != "lev" || !list[0].Authed {
		t.Fatalf("%+v", list)
	}
	if err := n.ForgeLogout(ctx, me, host); err != nil {
		t.Fatal(err)
	}
	if list, _ := n.ForgeAccounts(ctx, me); len(list) != 0 {
		t.Fatalf("after logout %+v", list)
	}
}

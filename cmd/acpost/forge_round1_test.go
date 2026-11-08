package main

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeForgejo is a Forgejo that knows one token ("good", user lev), over TLS; its certificate is
// in the file returned.
func fakeForgejo(t *testing.T) (host, certFile string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			w.Write([]byte(`{"version":"11.0.1"}`))
		case "/api/v1/user":
			if r.Header.Get("Authorization") != "token good" {
				w.WriteHeader(401)
				w.Write([]byte(`{"message":"token is invalid"}`))
				return
			}
			w.Write([]byte(`{"login":"lev"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	certFile = filepath.Join(t.TempDir(), "ca.pem")
	pemb := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(certFile, pemb, 0o600); err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(srv.URL, "https://"), certFile
}

// noKeychain puts a security(1) in PATH that keeps nothing and says where it was asked to: a
// login must never reach the user's real keychain from a test, and one that tries is seen.
func noKeychain(t *testing.T, e *env) string {
	t.Helper()
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "security"), []byte("#!/bin/sh\necho \"$1\" >> '"+d+"/calls'\nexit 0\n"), 0o755)
	for i, v := range e.vars {
		if strings.HasPrefix(v, "PATH=") {
			e.vars[i] = "PATH=" + d + ":" + strings.TrimPrefix(v, "PATH=")
		}
	}
	return filepath.Join(d, "calls")
}

// acpost forge login goes through the node: the token is kept where the node reads it (here
// ACPOST_SECRETS_FILE=1, a file), and only once the forge accepts it.
func TestForgeLoginThroughTheNode(t *testing.T) {
	host, cert := fakeForgejo(t)
	e := newEnv(t, inbox)
	calls := noKeychain(t, e)
	e.vars = append(e.vars, "ACPOST_SECRETS_FILE=1", "SSL_CERT_FILE="+cert)
	e.serve()
	tokens := filepath.Join(e.dir, ".config", "acpost", "forge-tokens.json")

	r := e.run("wrong\n", "forge", "login", host)
	if r.code == 0 {
		t.Fatalf("a token the forge refuses was kept: %+v", r)
	}
	if b, _ := os.ReadFile(tokens); strings.Contains(string(b), "wrong") {
		t.Fatalf("the refused token is in %s", tokens)
	}
	if b, _ := os.ReadFile(calls); strings.Contains(string(b), "add-generic-password") {
		t.Fatal("the CLI wrote to the keychain, where the node does not read")
	}
	if strings.Contains(r.err, "wrong") {
		t.Fatalf("the error quotes the token: %s", r.err)
	}

	if runtime.GOOS == "darwin" {
		// The node's TLS takes the system's roots only on macOS (SSL_CERT_FILE is not read):
		// the accepted half runs on Linux.
		return
	}
	e.ok("good\n", "forge", "login", host)
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "remote", "add", "origin", "https://" + host + "/o/r.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	var info struct {
		Authed bool `json:"authed"`
	}
	if err := json.Unmarshal([]byte(e.ok("", "forge", "status", "--dir", repo, "--json")), &info); err != nil || !info.Authed {
		t.Fatalf("forge status after login: %+v %v", info, err)
	}
	e.ok("", "forge", "logout", host)
	if b, _ := os.ReadFile(tokens); strings.Contains(string(b), "good") {
		t.Fatal("the token outlived logout")
	}
}

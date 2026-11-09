package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// status asks the local node for its health once: connect's probe is the answer it prints.
func TestStatusAsksOnce(t *testing.T) {
	e := newEnv(t, inbox)
	os.MkdirAll(filepath.Dir(e.sock), 0o700)
	ln, err := net.Listen("unix", e.sock)
	if err != nil {
		t.Fatal(err)
	}
	var health atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/health":
			health.Add(1)
			w.Write([]byte(`{"ok":true,"node":"n1","version":"v","you":"user:me"}`))
		case "/v1/agents":
			w.Write([]byte(`[]`))
		default:
			w.WriteHeader(404)
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	if out := e.ok("", "status"); !strings.Contains(out, "node n1") {
		t.Fatalf("%q", out)
	}
	if n := health.Load(); n != 1 {
		t.Fatalf("health asked %d times", n)
	}
}

// forge log --json is JSON, as every command's output is with --json.
func TestForgeLogJSON(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("line 1\nline 2\n"))
	}))
	defer node.Close()
	e := newEnv(t, inbox)
	var got struct{ Job, Log string }
	out := e.ok("", "--node", node.URL, "--json", "forge", "log", "51", "--dir", "/")
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Job != "51" || got.Log != "line 1\nline 2\n" {
		t.Fatalf("%q: %+v %v", out, got, err)
	}
}

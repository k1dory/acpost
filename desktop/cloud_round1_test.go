//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
)

// cloudWho is a cloud that knows one key.
func cloudWho(t *testing.T, key string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/whoami" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"tenant": "acme", "subject": "key:key_1"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Connecting a cloud: the key the cloud knows is written to a file of the user's alone, the
// configuration names it and keeps the rest, and the app's node starts again with it. A key the
// cloud refuses is kept nowhere and restarts nothing.
func TestCloudConnect(t *testing.T) {
	srv := cloudWho(t, "sbk_good")
	var mu sync.Mutex
	var clouds []*config.Cloud
	ownNode(t, nil)
	runNode = func(ctx context.Context, cfg *config.Config, _ *slog.Logger, ready func()) error {
		mu.Lock()
		clouds = append(clouds, cfg.Cloud)
		mu.Unlock()
		ready()
		<-ctx.Done()
		return nil
	}
	path := os.Getenv("ACPOST_CONFIG")
	before, _ := os.ReadFile(path)
	withAgents := string(before) + "# my agents\nagents:\n  echo: {driver: inbox} # kept\n"
	os.WriteFile(path, []byte(withAgents), 0o640)
	os.Chmod(path, 0o640)
	env := filepath.Join(filepath.Dir(path), cloudEnvName)

	n := &Node{emitFn: func(string, any) {}}
	t.Cleanup(func() { n.ServiceShutdown() })
	if err := n.ensureNode(); err != nil {
		t.Fatal(err)
	}

	if _, err := n.CloudConnect(srv.URL, "sbk_wrong", ""); err == nil || !strings.Contains(err.Error(), `"refused"`) {
		t.Fatalf("a wrong key: %v", err)
	}
	if _, err := n.CloudConnect("ftp://nowhere", "sbk_good", ""); err == nil || !strings.Contains(err.Error(), `"invalid"`) {
		t.Fatalf("not an address: %v", err)
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Fatalf("a refused key was kept: %v", err)
	}
	if now, _ := os.ReadFile(path); string(now) != withAgents {
		t.Fatalf("the configuration changed:\n%s", now)
	}
	mu.Lock()
	if len(clouds) != 1 || clouds[0] != nil {
		t.Fatalf("starts %d", len(clouds))
	}
	mu.Unlock()

	out, err := n.CloudConnect(" "+srv.URL+"/ ", " sbk_good\n", "")
	if err != nil || !out.Restarted || out.Info.Tenant != "acme" || out.Info.Subject != "key:key_1" {
		t.Fatalf("%+v %v", out, err)
	}
	fi, err := os.Stat(env)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	if b, _ := os.ReadFile(env); !strings.Contains(string(b), "SANDBOXD_URL="+srv.URL+"\n") || !strings.Contains(string(b), "SANDBOXD_TOKEN=sbk_good\n") {
		t.Fatalf("%s", b)
	}
	now, _ := os.ReadFile(path)
	if !strings.Contains(string(now), "# my agents") || !strings.Contains(string(now), "# kept") || !strings.Contains(string(now), "echo:") ||
		strings.Contains(string(now), "sbk_good") {
		t.Fatalf("the configuration:\n%s", now)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("the configuration's mode %v", fi.Mode())
	}
	mu.Lock()
	if len(clouds) != 2 || clouds[1] == nil || clouds[1].URL != srv.URL || clouds[1].Token != "sbk_good" {
		t.Fatalf("the node started again with %+v", clouds)
	}
	mu.Unlock()
	if n.ownNode() != nodeUp {
		t.Fatal("no node after the restart")
	}
	// Another key: the cloud's line is replaced, not doubled.
	srv2 := cloudWho(t, "sbk_next")
	if out, err := n.CloudConnect(srv2.URL, "sbk_next", ""); err != nil || !out.Restarted {
		t.Fatalf("%+v %v", out, err)
	}
	now, _ = os.ReadFile(path)
	if strings.Count(string(now), "cloud:") != 1 {
		t.Fatalf("the configuration:\n%s", now)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Cloud.URL != srv2.URL || cfg.Cloud.Token != "sbk_next" || cfg.Agents["echo"] == nil {
		t.Fatalf("%+v %v", cfg, err)
	}
}

// A node that is not the app's is not restarted by it: the key is written, and its owner told.
func TestCloudConnectAnothersNode(t *testing.T) {
	srv := cloudWho(t, "sbk_good")
	ownNode(t, func(ctx context.Context, ready func()) error { t.Error("a node started"); return nil })
	n := &Node{emitFn: func(string, any) {}}
	out, err := n.CloudConnect(srv.URL, "sbk_good", "")
	if err != nil || out.Restarted || out.Info.Tenant != "acme" {
		t.Fatalf("%+v %v", out, err)
	}
	if cfg, err := config.Load(os.Getenv("ACPOST_CONFIG")); err != nil || cfg.Cloud == nil || cfg.Cloud.Token != "sbk_good" {
		t.Fatalf("%+v %v", cfg, err)
	}
	t.Setenv("ACPOST_NODE", "https://hub.example")
	if _, err := n.CloudConnect(srv.URL, "sbk_good", ""); err == nil || !strings.Contains(err.Error(), "ACPOST_NODE") {
		t.Fatalf("on another node: %v", err)
	}
}

func TestSetCloudYAML(t *testing.T) {
	for _, c := range []struct{ name, src, ca, want string }{
		{"added", "# top\nagents:\n  a: {driver: exec} # x\n", "", "# top\nagents:\n  a: {driver: exec} # x\ncloud: {env_file: \"/e/cloud.env\"}\n"},
		{"replaced", "cloud:\n  url: https://old\n  token_env: T\nstore: s\n", "/ca.pem", "cloud: {env_file: \"/e/cloud.env\", ca: \"/ca.pem\"}\nstore: s\n"},
		{"empty", "", "", "cloud: {env_file: \"/e/cloud.env\"}\n"},
	} {
		got, err := setCloudYAML([]byte(c.src), "/e/cloud.env", c.ca)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %v\n%s", c.name, err, got)
		}
	}
	for _, src := range []string{"- a\n- b\n", "just text", "a: [\n"} {
		if got, err := setCloudYAML([]byte(src), "/e", ""); err == nil {
			t.Errorf("%q became %q", src, got)
		}
	}
}

// A configuration the node could not read with the cloud in it is left as it was, the key's file too.
func TestWriteCloudKeepsAWorkingConfiguration(t *testing.T) {
	dir := t.TempDir()
	path, env := filepath.Join(dir, "config.yaml"), filepath.Join(dir, cloudEnvName)
	src := "agents:\n  a: {driver: nonsense}\n"
	os.WriteFile(path, []byte(src), 0o600)
	os.WriteFile(env, []byte("SANDBOXD_URL=https://old\nSANDBOXD_TOKEN=old\n"), 0o600)
	if err := writeCloud(path, env, &config.Cloud{URL: "https://new", Token: "new"}); err == nil {
		t.Fatal("a configuration that does not load was written")
	}
	if b, _ := os.ReadFile(path); string(b) != src {
		t.Fatalf("%s", b)
	}
	if b, _ := os.ReadFile(env); !strings.Contains(string(b), "TOKEN=old") {
		t.Fatalf("%s", b)
	}
	if err := writeCloud(filepath.Join(dir, "config.json"), env, &config.Cloud{URL: "https://new", Token: "new"}); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("%v", err)
	}
}

// The configuration's write itself fails (the disk, not the YAML): the key that was there is the
// key that is there, the very file, and nothing of the new one stays.
func TestWriteCloudConfigWriteFails(t *testing.T) {
	dir := t.TempDir()
	path, env := filepath.Join(dir, "config.yaml"), filepath.Join(dir, cloudEnvName)
	src := "agents:\n  a: {driver: inbox}\ncloud: {env_file: \"" + env + "\"}\n"
	os.WriteFile(path, []byte(src), 0o600)
	os.WriteFile(env, []byte("SANDBOXD_URL=https://old\nSANDBOXD_TOKEN=old\n"), 0o600)
	before, _ := os.Stat(env)
	old := writeFile
	t.Cleanup(func() { writeFile = old })
	writeFile = func(p string, b []byte, perm os.FileMode, keep *os.FileMode, replace bool) error {
		if p == path {
			return errors.New("no space left on device")
		}
		return old(p, b, perm, keep, replace)
	}
	err := writeCloud(path, env, &config.Cloud{URL: "https://new", Token: "new"})
	if err == nil || !strings.Contains(err.Error(), "no space") {
		t.Fatalf("%v", err)
	}
	after, _ := os.Stat(env)
	if b, _ := os.ReadFile(env); !strings.Contains(string(b), "TOKEN=old") || !os.SameFile(before, after) {
		t.Fatalf("the key's file is not the one that was there: %s", b)
	}
	if b, _ := os.ReadFile(path); string(b) != src {
		t.Fatalf("%s", b)
	}
	if list, _ := os.ReadDir(dir); len(list) != 2 {
		t.Fatalf("left behind: %v", list)
	}
	// No key's file before: none after.
	os.Remove(env)
	if err := writeCloud(path, env, &config.Cloud{URL: "https://new", Token: "new"}); err == nil {
		t.Fatal("written")
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Fatalf("a key was left: %v", err)
	}
	// The key's write fails: the same.
	os.WriteFile(env, []byte("SANDBOXD_URL=https://old\nSANDBOXD_TOKEN=old\n"), 0o600)
	writeFile = func(p string, b []byte, perm os.FileMode, keep *os.FileMode, replace bool) error {
		return errors.New("read-only file system")
	}
	if err := writeCloud(path, env, &config.Cloud{URL: "https://new", Token: "new"}); err == nil {
		t.Fatal("written")
	}
	if b, _ := os.ReadFile(env); !strings.Contains(string(b), "TOKEN=old") {
		t.Fatalf("%s", b)
	}
	// A key's file that cannot be looked at is neither written over nor removed.
	writeFile = old
	locked := filepath.Join(dir, "locked")
	os.Mkdir(locked, 0o700)
	os.WriteFile(filepath.Join(locked, cloudEnvName), []byte("SANDBOXD_TOKEN=old\n"), 0o600)
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if err := writeCloud(path, filepath.Join(locked, cloudEnvName), &config.Cloud{URL: "https://new", Token: "new"}); err == nil {
		t.Fatal("written over a file that could not be read")
	}
	os.Chmod(locked, 0o700)
	if b, _ := os.ReadFile(filepath.Join(locked, cloudEnvName)); string(b) != "SANDBOXD_TOKEN=old\n" {
		t.Fatalf("%q", b)
	}
}

// Two connects at once, each to its own cloud with its own CA: what is left is one of them whole,
// the address, the key and the CA of the same cloud.
func TestCloudConnectAtOnce(t *testing.T) {
	ownNode(t, func(ctx context.Context, ready func()) error { ready(); <-ctx.Done(); return nil })
	dir := t.TempDir()
	type cloud struct{ url, key, ca string }
	var clouds []cloud
	for _, k := range []string{"sbk_a", "sbk_b"} {
		key := k
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+key {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"tenant": key})
		}))
		t.Cleanup(srv.Close)
		ca := filepath.Join(dir, key+".pem")
		os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
		clouds = append(clouds, cloud{srv.URL, key, ca})
	}
	n := &Node{emitFn: func(string, any) {}}
	t.Cleanup(func() { n.ServiceShutdown() })
	if err := n.ensureNode(); err != nil {
		t.Fatal(err)
	}
	// Each write is slow enough for the other connect to come between the key and the configuration.
	old := writeFile
	t.Cleanup(func() { writeFile = old })
	writeFile = func(p string, b []byte, perm os.FileMode, keep *os.FileMode, replace bool) error {
		time.Sleep(20 * time.Millisecond)
		return old(p, b, perm, keep, replace)
	}
	for range 5 {
		var wg sync.WaitGroup
		for _, c := range clouds {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := n.CloudConnect(c.url, c.key, c.ca); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		cfg, err := config.Load(os.Getenv("ACPOST_CONFIG"))
		if err != nil {
			t.Fatal(err)
		}
		got := cloud{cfg.Cloud.URL, cfg.Cloud.Token, cfg.Cloud.CA}
		if got != clouds[0] && got != clouds[1] {
			t.Fatalf("a mix of two clouds: %+v", got)
		}
	}
}

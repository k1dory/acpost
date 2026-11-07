package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var ctx = context.Background()

func TestParseRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:k1dory/acpost.git":              "github.com k1dory/acpost",
		"https://gitlab.example.com/group/sub/proj.git": "gitlab.example.com group/sub/proj",
		"ssh://git@forgejo.example.ru:2222/infra/docs":  "forgejo.example.ru infra/docs",
		"https://forge.local:3000/o/r":                  "forge.local:3000 o/r",
	} {
		r, err := ParseRemote(in)
		if err != nil || r.Host+" "+r.Path != want {
			t.Fatalf("%s: %+v %v", in, r, err)
		}
	}
	for _, bad := range []string{"", "nohost", "https://github.com/onlyowner"} {
		if _, err := ParseRemote(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}

// fake is a forge of a kind on a TLS test server; it records the authorization it was given.
func fake(t *testing.T, kind string, routes map[string]any) (*Client, string, *[]string) {
	var auths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization")+r.Header.Get("PRIVATE-TOKEN"))
		key := r.Method + " " + r.URL.EscapedPath()
		v, ok := routes[key]
		if !ok {
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"no ` + key + `"}`))
			return
		}
		switch x := v.(type) {
		case int:
			w.WriteHeader(x)
		case string:
			w.Write([]byte(x))
		case http.HandlerFunc:
			x(w, r)
		default:
			json.NewEncoder(w).Encode(x)
		}
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	c := &Client{HTTP: srv.Client(), Token: func(string) string { return "tok" }, Kind: func(string) string { return kind }}
	return c, host, &auths
}

func TestForgejo(t *testing.T) {
	c, host, auths := fake(t, "", map[string]any{
		"GET /api/v1/version": map[string]string{"version": "11.0.1+gitea-1.22.0"},
		"GET /api/v1/repos/o/r/pulls": []map[string]any{{"number": 3, "title": "fix", "state": "open",
			"user": map[string]string{"login": "lev"}, "head": map[string]string{"ref": "feat"}, "base": map[string]string{"ref": "main"}, "html_url": "u"}},
		"GET /api/v1/repos/o/r/commits/feat/statuses": []map[string]any{
			{"id": 1, "context": "ci / test", "status": "pending", "updated_at": "2026-11-07T01:00:00Z"},
			{"id": 2, "context": "ci / test", "status": "failure", "updated_at": "2026-11-07T01:05:00Z"},
		},
		"POST /api/v1/repos/o/r/pulls": map[string]any{"number": 4, "html_url": "u4"},
	})
	c.Kind = func(string) string { return "" } // asked of the host
	srv, err := c.Probe(ctx, host)
	if err != nil || srv.Kind != Forgejo || srv.Version != "11.0.1+gitea-1.22.0" {
		t.Fatalf("%+v %v", srv, err)
	}
	r := Repo{Host: host, Path: "o/r"}
	pulls, err := c.Pulls(ctx, srv, r, "open")
	if err != nil || len(pulls) != 1 || pulls[0].Head != "feat" || pulls[0].Author != "lev" {
		t.Fatalf("%+v %v", pulls, err)
	}
	checks, err := c.Checks(ctx, srv, r, "feat")
	if err != nil || len(checks) != 1 || checks[0].Status != "failure" {
		t.Fatalf("latest status per context: %+v %v", checks, err)
	}
	if _, err := c.Log(ctx, srv, r, "7", "https://x/o/r/actions/runs/1/jobs/0"); err == nil || !strings.Contains(err.Error(), "version 11.0.1") {
		t.Fatalf("log of a forge without it: %v", err)
	}
	if p, err := c.OpenPull(ctx, srv, r, PullRequest{Head: "feat", Base: "main", Title: "t"}); err != nil || p.Number != 4 {
		t.Fatalf("%+v %v", p, err)
	}
	if (*auths)[len(*auths)-1] != "token tok" {
		t.Fatalf("auth %q", (*auths)[len(*auths)-1])
	}
}

func TestGitLab(t *testing.T) {
	c, host, auths := fake(t, "", map[string]any{
		"GET /api/v1/version": 404,
		"GET /api/v4/version": 401, // only with a token: GitLab all the same
		"GET /api/v4/projects/group%2Fsub%2Fproj/merge_requests":    []map[string]any{{"iid": 9, "title": "mr", "state": "opened", "source_branch": "b", "target_branch": "main", "author": map[string]string{"username": "u"}}},
		"GET /api/v4/projects/group%2Fsub%2Fproj/pipelines":         []map[string]any{{"id": 77, "status": "failed", "ref": "b"}},
		"GET /api/v4/projects/group%2Fsub%2Fproj/pipelines/77/jobs": []map[string]any{{"id": 5, "name": "unit", "stage": "test", "status": "failed"}},
		"GET /api/v4/projects/group%2Fsub%2Fproj/jobs/5/trace":      "line 1\nERROR: boom\n",
	})
	c.Kind = func(string) string { return "" }
	srv, err := c.Probe(ctx, host)
	if err != nil || srv.Kind != GitLab {
		t.Fatalf("%+v %v", srv, err)
	}
	r := Repo{Host: host, Path: "group/sub/proj"}
	if pulls, err := c.Pulls(ctx, srv, r, "open"); err != nil || len(pulls) != 1 || pulls[0].Number != 9 || pulls[0].State != "open" {
		t.Fatalf("%+v %v", pulls, err)
	}
	checks, err := c.Checks(ctx, srv, r, "b")
	if err != nil || len(checks) != 1 || checks[0].Status != "failure" || len(checks[0].Jobs) != 1 || checks[0].Jobs[0].Name != "test / unit" {
		t.Fatalf("%+v %v", checks, err)
	}
	if text, err := c.Log(ctx, srv, r, "5", ""); err != nil || !strings.Contains(text, "ERROR: boom") {
		t.Fatalf("%q %v", text, err)
	}
	if (*auths)[len(*auths)-1] != "tok" {
		t.Fatalf("PRIVATE-TOKEN not sent: %q", (*auths)[len(*auths)-1])
	}
}

func TestGitHubEnterprise(t *testing.T) {
	c, host, auths := fake(t, GitHub, map[string]any{
		"GET /api/v3/meta": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-GitHub-Enterprise-Version", "3.14.2")
			w.Write([]byte("{}"))
		}),
		"GET /api/v3/repos/o/r/actions/runs":         map[string]any{"workflow_runs": []map[string]any{{"id": 11, "name": "ci", "status": "completed", "conclusion": "failure", "head_branch": "b"}}},
		"GET /api/v3/repos/o/r/actions/runs/11/jobs": map[string]any{"jobs": []map[string]any{{"id": 12, "name": "test", "status": "completed", "conclusion": "failure"}}},
		"GET /api/v3/repos/o/r/actions/jobs/12/logs": "\ufeffstep 1\n##[error]failed\n",
	})
	srv, err := c.Probe(ctx, host)
	if err != nil || srv.Kind != GitHub || srv.Version != "3.14.2" || !strings.HasSuffix(srv.API, "/api/v3") {
		t.Fatalf("%+v %v", srv, err)
	}
	r := Repo{Host: host, Path: "o/r"}
	checks, err := c.Checks(ctx, srv, r, "b")
	if err != nil || len(checks) != 1 || checks[0].Status != "failure" || checks[0].Jobs[0].ID != "12" {
		t.Fatalf("%+v %v", checks, err)
	}
	if text, _ := c.Log(ctx, srv, r, "12", ""); !strings.HasPrefix(text, "step 1") {
		t.Fatalf("%q", text)
	}
	if (*auths)[0] != "Bearer tok" {
		t.Fatalf("%q", (*auths)[0])
	}
}

func TestUnknownHost(t *testing.T) {
	c, host, _ := fake(t, "", map[string]any{})
	c.Kind = func(string) string { return "" }
	if _, err := c.Probe(ctx, host); err == nil || !strings.Contains(err.Error(), "not a forge acpost knows") {
		t.Fatalf("%v", err)
	}
}

// A remote's path goes into API paths: only plain names.
func TestParseRemoteRefusesInjection(t *testing.T) {
	for _, bad := range []string{"https://github.com/o/r/merges%23/x.git", "https://github.com/o/r?x=1", "git@github.com:o/../r", "https://evil.com@github.com/o/r#x"} {
		if r, err := ParseRemote(bad); err == nil {
			t.Fatalf("%q parsed as %+v", bad, r)
		}
	}
}

// A redirect elsewhere goes without the token; to http, not at all.
func TestRedirectKeepsTheToken(t *testing.T) {
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("PRIVATE-TOKEN") != "" {
			w.WriteHeader(500)
			return
		}
		w.Write([]byte("log text"))
	}))
	defer other.Close()
	c, host, _ := fake(t, GitLab, map[string]any{
		"GET /api/v4/projects/o%2Fr/jobs/1/trace": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/blob", http.StatusFound)
		}),
		"GET /api/v4/projects/o%2Fr/jobs/2/trace": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://"+strings.TrimPrefix(other.URL, "https://")+"/blob", http.StatusFound)
		}),
	})
	c.HTTP.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true // the other test server
	srv := Server{Kind: GitLab, API: "https://" + host + "/api/v4"}
	r := Repo{Host: host, Path: "o/r"}
	if text, err := c.Log(ctx, srv, r, "1", ""); err != nil || text != "log text" {
		t.Fatalf("%q %v", text, err)
	}
	if _, err := c.Log(ctx, srv, r, "2", ""); err == nil {
		t.Fatal("followed a redirect to http")
	}
}

// A big log keeps its end, and says its start is cut.
func TestLogKeepsTheEnd(t *testing.T) {
	big := strings.Repeat("line\n", logMax/5+1000) + "FINAL ERROR\n"
	c, host, _ := fake(t, GitLab, map[string]any{"GET /api/v4/projects/o%2Fr/jobs/1/trace": big})
	text, err := c.Log(ctx, Server{Kind: GitLab, API: "https://" + host + "/api/v4"}, Repo{Host: host, Path: "o/r"}, "1", "")
	if err != nil || !strings.HasSuffix(text, "FINAL ERROR\n") || !strings.HasPrefix(text, "… (the log's start is cut") {
		t.Fatalf("%v %q…%q", err, text[:40], text[len(text)-20:])
	}
}

// GitHub Enterprise is found without being named.
func TestProbeFindsEnterprise(t *testing.T) {
	c, host, _ := fake(t, "", map[string]any{
		"GET /api/v3/meta": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-GitHub-Enterprise-Version", "3.15.0")
			w.Write([]byte("{}"))
		}),
	})
	c.Kind = func(string) string { return "" }
	if srv, err := c.Probe(ctx, host); err != nil || srv.Kind != GitHub || srv.Version != "3.15.0" {
		t.Fatalf("%+v %v", srv, err)
	}
}

// Logins from processes at once keep every token (a file store, under its lock).
func TestStoreConcurrentLogins(t *testing.T) {
	dir := t.TempDir()
	done := make(chan error, 20)
	for i := range 20 {
		go func() {
			s := &Store{Dir: dir, File: true} // a store each: as separate processes
			done <- s.Login("h"+string(rune('a'+i))+".example", "", "tok"+string(rune('a'+i)))
		}()
	}
	for range 20 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{Dir: dir, File: true}
	for i := range 20 {
		if s.Token("h"+string(rune('a'+i))+".example") != "tok"+string(rune('a'+i)) {
			t.Fatalf("token %d lost", i)
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, "forge-tokens.json")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

// Gitea with GitHub's Actions API: runs, jobs and a job's log by its id; a commit status (no
// Actions API) has no log; a draft is a WIP: title; a full SHA is head_sha on GitHub.
func TestGiteaActionsAndDrafts(t *testing.T) {
	var gotTitle, gotQuery string
	c, host, _ := fake(t, Forgejo, map[string]any{
		"GET /api/v1/repos/o/r/actions/runs": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []map[string]any{{"id": 5, "display_title": "ci", "status": "completed", "conclusion": "failure"}}})
		}),
		"GET /api/v1/repos/o/r/actions/runs/5/jobs":  map[string]any{"jobs": []map[string]any{{"id": 51, "name": "test", "status": "completed", "conclusion": "failure"}}},
		"GET /api/v1/repos/o/r/actions/jobs/51/logs": "boom\n",
		"POST /api/v1/repos/o/r/pulls": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			gotTitle, _ = b["title"].(string)
			json.NewEncoder(w).Encode(map[string]any{"number": 1})
		}),
	})
	srv := Server{Kind: Forgejo, API: "https://" + host + "/api/v1", Version: "1.24.0"}
	r := Repo{Host: host, Path: "o/r"}
	checks, err := c.Checks(ctx, srv, r, "feat")
	if err != nil || len(checks) != 1 || checks[0].Name != "ci" || checks[0].Jobs[0].ID != "51" || gotQuery != "limit=10&branch=feat" {
		t.Fatalf("%+v %v %q", checks, err, gotQuery)
	}
	if text, err := c.Log(ctx, srv, r, "51", ""); err != nil || text != "boom\n" {
		t.Fatalf("%q %v", text, err)
	}
	if _, err := c.Log(ctx, srv, r, "status:3", "https://x/run"); err == nil || !strings.Contains(err.Error(), "https://x/run") {
		t.Fatalf("a status's log: %v", err)
	}
	if _, err := c.OpenPull(ctx, srv, r, PullRequest{Head: "h", Base: "main", Title: "t", Draft: true}); err != nil || gotTitle != "WIP: t" {
		t.Fatalf("%q %v", gotTitle, err)
	}
	sha := strings.Repeat("ab", 20)
	c.Checks(ctx, srv, r, sha)
	if gotQuery != "limit=10&head_sha="+sha {
		t.Fatalf("%q", gotQuery)
	}
}

// The token a request carries is never in its error: a body that repeats it (past the shown
// length too), a redirect whose URL holds it.
func TestErrorsHideTheToken(t *testing.T) {
	const secret = "s3cr3t0123456789abcdef0123456789abcdef01"
	c, host, _ := fake(t, Forgejo, map[string]any{
		"GET /api/v1/version": `{"version":"11"}`,
		"GET /api/v1/user": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte(strings.Repeat("x", 280) + secret))
		}),
		"GET /api/v1/repos/o/e": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte(`{"message":"\u0073` + secret[1:] + `"}`))
		}),
		"GET /api/v1/repos/o/h": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://"+secret+".example/", http.StatusFound)
		}),
		"GET /api/v1/repos/o/m": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://logs.example/%zz?sig=LOGSECRET42")
			w.WriteHeader(http.StatusFound)
		}),
		"GET /api/v1/repos/o/r": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://elsewhere.example/?token="+secret, http.StatusFound)
		}),
	})
	c.Token = func(string) string { return secret }
	srv, err := c.Probe(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Whoami(context.Background(), srv, host); err == nil || strings.Contains(err.Error(), secret[:12]) {
		t.Fatalf("a long body: %v", err)
	}
	var v map[string]any
	if _, err := c.get(context.Background(), srv, host, "/repos/o/r", &v, 1<<16); err == nil || strings.Contains(err.Error(), secret[:12]) || strings.Contains(err.Error(), "?") {
		t.Fatalf("a redirect: %v", err)
	}
	if _, err := c.get(context.Background(), srv, host, "/repos/o/e", &v, 1<<16); err == nil || strings.Contains(err.Error(), secret[:12]) {
		t.Fatalf("an escaped token: %v", err)
	}
	if _, err := c.get(context.Background(), srv, host, "/repos/o/m", &v, 1<<16); err == nil || strings.Contains(err.Error(), "LOGSECRET") {
		t.Fatalf("a malformed redirect: %v", err)
	}
	if _, err := c.get(context.Background(), srv, host, "/repos/o/h", &v, 1<<16); err == nil || strings.Contains(err.Error(), secret[:12]) {
		t.Fatalf("a token as the redirect's host: %v", err)
	}
}

// A remote refused is quoted without what may be a credential in it.
func TestParseRemoteHidesCredentials(t *testing.T) {
	for _, r := range []string{"https://user:SECRETPW@forge.example/o/r?x=1", "https://SECRETPW@bad_host!/o/r", "https://user:SECRETPW@forge.example/o", "git@forge.example:o/r.git?token=SECRETPW", "git@forge.example:o/r#SECRETPW"} {
		if _, err := ParseRemote(r); err == nil || strings.Contains(err.Error(), "SECRETPW") {
			t.Fatalf("%s: %v", r, err)
		}
	}
}

package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// routes are the patterns Handler registers ("METHOD /path"), read from its source: the router is
// the source of truth for the API.
func routes(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || len(c.Args) == 0 {
			return true
		}
		if sel, ok := c.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		if lit, ok := c.Args[0].(*ast.BasicLit); ok {
			if p, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(p, " ") {
				out = append(out, p)
			}
		}
		return true
	})
	if len(out) < 50 {
		t.Fatalf("only %d routes found in server.go", len(out))
	}
	return out
}

// documented are the routes the README's HTTP API section names: `GET /a` or `GET/POST /a?x=`.
func documented(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	start := strings.Index(s, "\n## HTTP API\n")
	if start < 0 {
		t.Fatal("README has no HTTP API section")
	}
	s = s[start+1:]
	if end := strings.Index(s[3:], "\n## "); end >= 0 {
		s = s[:end+3]
	}
	span := regexp.MustCompile("`([A-Z]+(?:/[A-Z]+)*) (/v1/[^`?\\s]*)[^`]*`")
	out := map[string]bool{}
	for _, m := range span.FindAllStringSubmatch(s, -1) {
		for _, method := range strings.Split(m[1], "/") {
			out[method+" "+m[2]] = true
		}
	}
	return out
}

// Every route the API serves is in the README's table, and the table names none it does not serve.
func TestReadmeDocumentsEveryRoute(t *testing.T) {
	doc := documented(t)
	served := map[string]bool{}
	for _, r := range routes(t) {
		served[r] = true
		if !doc[r] {
			t.Errorf("README HTTP API does not document %s", r)
		}
	}
	for r := range doc {
		if !served[r] {
			t.Errorf("README HTTP API documents %s, which is not served", r)
		}
	}
}

// The error codes the API answers with are listed with their statuses.
func TestReadmeListsErrorCodes(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for code := range statusOf {
		if !strings.Contains(s, "`"+code+"`") {
			t.Errorf("README does not list the error code %s", code)
		}
	}
	for _, c := range []string{"`internal`", "`agent_…`", "`session_…`", "501", "502"} {
		if !strings.Contains(s, c) {
			t.Errorf("README does not mention %s", c)
		}
	}
}

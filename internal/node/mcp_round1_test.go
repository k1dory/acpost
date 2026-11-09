package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// A server whose secrets cannot be read (a locked keychain; here a broken file) is left out of the
// session: the agent never gets it with an empty token in place of the one kept.
func TestMCPUnreadSecretsLeaveTheServerOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ACPOST_SECRETS_FILE", "1")
	n := acpNode(t, "")
	if err := n.PutMCPServer(ctx, me, store.MCPServer{Name: "wiki", Kind: "stdio", Command: "wiki-mcp", Env: []string{"TOKEN"}, Enabled: true},
		map[string]string{"env/TOKEN": "s3cret"}); err != nil {
		t.Fatal(err)
	}
	if err := n.PutMCPServer(ctx, me, store.MCPServer{Name: "plain", Kind: "stdio", Command: "plain-mcp", Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".config", "acpost", "acpost-mcp.secrets.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ask(t, n, SendRequest{Body: "mcp"}).Reply
	if strings.Contains(got, "wiki") {
		t.Fatalf("a server whose secrets were not read reached the agent: %q", got)
	}
	if !strings.Contains(got, "plain:plain-mcp") {
		t.Fatalf("a server without secrets went too: %q", got)
	}
}

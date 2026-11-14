package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// MCP servers the node's user gives its agents: every ACP session they open has them. Their
// environment's and headers' values are secrets, kept apart (the keychain); here, their names.

const mcpSchema = `
CREATE TABLE IF NOT EXISTS mcp_servers (
	name    TEXT    PRIMARY KEY,
	kind    TEXT    NOT NULL,              -- stdio, http
	command TEXT    NOT NULL DEFAULT '',
	args    TEXT    NOT NULL DEFAULT '[]', -- JSON
	url     TEXT    NOT NULL DEFAULT '',
	env     TEXT    NOT NULL DEFAULT '[]', -- JSON: the variables' names
	headers TEXT    NOT NULL DEFAULT '[]', -- JSON: the headers' names
	agents  TEXT    NOT NULL DEFAULT '[]', -- JSON: the agents that get it (none: all)
	enabled INTEGER NOT NULL DEFAULT 1,
	ts      INTEGER NOT NULL
) WITHOUT ROWID;
-- Secrets a change or a deletion could not forget: forgotten again at the next one.
CREATE TABLE IF NOT EXISTS mcp_orphans (
	account TEXT PRIMARY KEY
) WITHOUT ROWID;
`

// migrateMCP adds the columns added after the table first shipped.
func migrateMCP(s *Store) error {
	var n int
	if err := s.w.QueryRow(`SELECT count(*) FROM pragma_table_info('mcp_servers') WHERE name = 'secret_ver'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := s.w.Exec(`ALTER TABLE mcp_servers ADD COLUMN secret_ver TEXT NOT NULL DEFAULT ''`)
		return err
	}
	return nil
}

// MCPServer is an MCP server the agents get.
type MCPServer struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"` // stdio, http
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
	Env     []string `json:"env,omitempty"`     // names (values: secrets)
	Headers []string `json:"headers,omitempty"` // names (values: secrets)
	Agents  []string `json:"agents,omitempty"`  // none: every agent
	Enabled bool     `json:"enabled"`
	// SecretVer names the version of its secrets the server uses (they are kept per version: a
	// change publishes a new one in one step).
	SecretVer string `json:"-"`
}

var mcpName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// CheckMCPName: a server's name as the store keeps it.
func CheckMCPName(name string) error {
	if !mcpName.MatchString(name) {
		return fmt.Errorf("name %q: letters, digits, _ . - (64 at most)", name)
	}
	return nil
}

var mcpKey = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// Check says what is wrong with a server, if anything.
func (m MCPServer) Check() error {
	if !mcpName.MatchString(m.Name) {
		return fmt.Errorf("an MCP server's name: letters, digits, . _ - (up to 64)")
	}
	switch m.Kind {
	case "stdio":
		if strings.TrimSpace(m.Command) == "" {
			return fmt.Errorf("%s: a command to run", m.Name)
		}
	case "http":
		if !strings.HasPrefix(m.URL, "https://") && !strings.HasPrefix(m.URL, "http://") {
			return fmt.Errorf("%s: an http(s) URL", m.Name)
		}
	default:
		return fmt.Errorf("%s: kind stdio or http", m.Name)
	}
	for _, k := range append(append([]string{}, m.Env...), m.Headers...) {
		if !mcpKey.MatchString(k) {
			return fmt.Errorf("%s: %q is not a variable or header name", m.Name, k)
		}
	}
	if len(m.Args) > 64 || len(m.Env) > 64 || len(m.Headers) > 64 {
		return fmt.Errorf("%s: too many arguments, variables or headers", m.Name)
	}
	return nil
}

// MCPServers lists the servers, by name.
func (s *Store) MCPServers(ctx context.Context) ([]MCPServer, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT name, kind, command, args, url, env, headers, agents, enabled, secret_ver FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MCPServer{}
	for rows.Next() {
		var m MCPServer
		var args, env, headers, agents string
		if err := rows.Scan(&m.Name, &m.Kind, &m.Command, &args, &m.URL, &env, &headers, &agents, &m.Enabled, &m.SecretVer); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(args), &m.Args)
		_ = json.Unmarshal([]byte(env), &m.Env)
		_ = json.Unmarshal([]byte(headers), &m.Headers)
		_ = json.Unmarshal([]byte(agents), &m.Agents)
		out = append(out, m)
	}
	return out, rows.Err()
}

// PutMCPServer adds or replaces a server.
func (s *Store) PutMCPServer(ctx context.Context, m MCPServer) error {
	if err := m.Check(); err != nil {
		return err
	}
	j := func(v []string) string {
		if v == nil {
			v = []string{}
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO mcp_servers (name, kind, command, args, url, env, headers, agents, enabled, secret_ver, ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET kind = excluded.kind, command = excluded.command, args = excluded.args,
			url = excluded.url, env = excluded.env, headers = excluded.headers, agents = excluded.agents,
			enabled = excluded.enabled, secret_ver = excluded.secret_ver, ts = excluded.ts`,
		m.Name, m.Kind, m.Command, j(m.Args), m.URL, j(m.Env), j(m.Headers), j(m.Agents), m.Enabled, m.SecretVer, time.Now().UnixMilli())
	return err
}

// ErrNoMCPServer: no such server.
var ErrNoMCPServer = errors.New("no such MCP server")

// DeleteMCPServer removes a server.
func (s *Store) DeleteMCPServer(ctx context.Context, name string) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM mcp_servers WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNoMCPServer
	}
	return nil
}

// MCPOrphans lists the secrets to forget still.
func (s *Store) MCPOrphans(ctx context.Context) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT account FROM mcp_orphans`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetMCPOrphan notes a secret to forget (gone: forgotten).
func (s *Store) SetMCPOrphan(ctx context.Context, account string, gone bool) error {
	if gone {
		_, err := s.w.ExecContext(ctx, `DELETE FROM mcp_orphans WHERE account = ?`, account)
		return err
	}
	_, err := s.w.ExecContext(ctx, `INSERT OR IGNORE INTO mcp_orphans (account) VALUES (?)`, account)
	return err
}

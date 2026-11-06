// Package config reads a node's configuration (YAML or JSON) and fills in the defaults.
package config

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is a node's configuration.
type Config struct {
	Node      Node              `yaml:"node" json:"node"`
	Store     string            `yaml:"store" json:"store"`
	Retention Duration          `yaml:"retention" json:"retention"`
	Limits    Limits            `yaml:"limits" json:"limits"`
	Tokens    []*Token          `yaml:"tokens" json:"tokens"`
	Agents    map[string]*Agent `yaml:"agents" json:"agents"`
}

type Node struct {
	Name   string   `yaml:"name" json:"name"`
	Listen []string `yaml:"listen" json:"listen"`
	TLS    *TLS     `yaml:"tls" json:"tls"`
	Join   *Join    `yaml:"join" json:"join"`
}

type TLS struct {
	Cert string `yaml:"cert" json:"cert"`
	Key  string `yaml:"key" json:"key"`
}

// Join is the hub a node joins.
type Join struct {
	URL      string `yaml:"url" json:"url"`
	Token    string `yaml:"token" json:"token"`
	TokenEnv string `yaml:"token_env" json:"token_env"`
	CA       string `yaml:"ca" json:"ca"`
}

type Limits struct {
	MaxBody             Size `yaml:"max_body" json:"max_body"`
	MaxReply            Size `yaml:"max_reply" json:"max_reply"`
	MaxMeta             Size `yaml:"max_meta" json:"max_meta"`
	MaxFiles            int  `yaml:"max_files" json:"max_files"`           // files attached to a message, and to its reply
	MaxFilesSize        Size `yaml:"max_files_size" json:"max_files_size"` // their bytes together, each side
	MaxDepth            int  `yaml:"max_depth" json:"max_depth"`
	MaxQueuedPerAgent   int  `yaml:"max_queued_per_agent" json:"max_queued_per_agent"`
	MaxEventsPerMessage int  `yaml:"max_events_per_message" json:"max_events_per_message"`
	MaxStore            Size `yaml:"max_store" json:"max_store"`
	MaxAttempts         int  `yaml:"max_attempts" json:"max_attempts"`
}

// Token lets a program reach the node over TCP.
type Token struct {
	Name     string   `yaml:"name" json:"name"`
	SHA256   string   `yaml:"sha256" json:"sha256"`
	TokenEnv string   `yaml:"token_env" json:"token_env"`
	SendTo   []string `yaml:"send_to" json:"send_to"`
	ActAs    []string `yaml:"act_as" json:"act_as"`
	Node     bool     `yaml:"node" json:"node"` // a node that joins this one as its hub
	hash     []byte
}

// Matches says whether secret is this token (in constant time).
func (t *Token) Matches(secret string) bool {
	h := sha256.Sum256([]byte(secret))
	return len(t.hash) == len(h) && subtle.ConstantTimeCompare(h[:], t.hash) == 1
}

// Agent is one mailbox and how it is served.
type Agent struct {
	Driver       string            `yaml:"driver" json:"driver"`
	Command      []string          `yaml:"command" json:"command"`
	Cwd          string            `yaml:"cwd" json:"cwd"`
	Env          map[string]string `yaml:"env" json:"env"`
	AuthMethod   string            `yaml:"auth_method" json:"auth_method"`
	Mode         string            `yaml:"mode" json:"mode"`
	Model        string            `yaml:"model" json:"model"`
	ThoughtLevel string            `yaml:"thought_level" json:"thought_level"`
	Permissions  string            `yaml:"permissions" json:"permissions"`
	Session      string            `yaml:"session" json:"session"`
	Reply        string            `yaml:"reply" json:"reply"`
	Concurrency  int               `yaml:"concurrency" json:"concurrency"`
	TurnTimeout  Duration          `yaml:"turn_timeout" json:"turn_timeout"`
	IdleTimeout  Duration          `yaml:"idle_timeout" json:"idle_timeout"`
	Preamble     *bool             `yaml:"preamble" json:"preamble"`
	AllowFrom    []string          `yaml:"allow_from" json:"allow_from"`
	Lease        Duration          `yaml:"lease" json:"lease"`
}

// PreambleOn says whether prompts start with the preamble.
func (a *Agent) PreambleOn() bool { return a.Preamble == nil || *a.Preamble }

// StateDir is where a node keeps its socket and store by default.
func StateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "acpost")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "acpost")
}

// DefaultPath is the configuration file read when none is named.
func DefaultPath() string {
	if p := os.Getenv("ACPOST_CONFIG"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "acpost", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "acpost", "config.yaml")
}

// Load reads a configuration file; a file that does not exist is the defaults.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Parse(nil, ".yaml")
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(b, filepath.Ext(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse reads a configuration: JSON when ext is ".json", YAML otherwise. Unknown fields are errors.
func Parse(b []byte, ext string) (*Config, error) {
	c := &Config{}
	if strings.EqualFold(ext, ".json") {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(c); err != nil {
			return nil, err
		}
	} else if len(bytes.TrimSpace(b)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(c); err != nil {
			return nil, err
		}
	}
	if err := c.fill(); err != nil {
		return nil, err
	}
	return c, nil
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName says whether s can name an agent, a node or a token.
func ValidName(s string) bool { return nameRE.MatchString(s) }

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

func (c *Config) fill() error {
	state := StateDir()
	if c.Node.Name == "" {
		h, _ := os.Hostname()
		c.Node.Name = strings.SplitN(h, ".", 2)[0]
		if !ValidName(c.Node.Name) {
			c.Node.Name = "node"
		}
	}
	if !ValidName(c.Node.Name) {
		return fmt.Errorf("node.name %q: letters, digits, . _ -", c.Node.Name)
	}
	if len(c.Node.Listen) == 0 {
		c.Node.Listen = []string{"unix://" + filepath.Join(state, "acpost.sock")}
	}
	for i, l := range c.Node.Listen {
		switch {
		case strings.HasPrefix(l, "unix://"):
			c.Node.Listen[i] = "unix://" + expand(strings.TrimPrefix(l, "unix://"))
		case strings.HasPrefix(l, "tcp://"):
			host, _, err := net.SplitHostPort(strings.TrimPrefix(l, "tcp://"))
			if err != nil {
				return fmt.Errorf("node.listen %q: %v", l, err)
			}
			ip := net.ParseIP(host)
			loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
			if !loopback && c.Node.TLS == nil {
				return fmt.Errorf("node.listen %q: a non-loopback address needs node.tls", l)
			}
		default:
			return fmt.Errorf("node.listen %q: unix:///path or tcp://host:port", l)
		}
	}
	if t := c.Node.TLS; t != nil {
		if t.Cert == "" || t.Key == "" {
			return errors.New("node.tls: cert and key")
		}
		t.Cert, t.Key = expand(t.Cert), expand(t.Key)
	}
	if j := c.Node.Join; j != nil {
		u, err := url.Parse(j.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("node.join.url %q: http(s)://host:port", j.URL)
		}
		if j.TokenEnv != "" {
			j.Token = os.Getenv(j.TokenEnv)
		}
		if j.Token == "" {
			return errors.New("node.join: token or token_env (set) needed")
		}
		j.CA = expand(j.CA)
	}
	switch {
	case c.Store == "":
		c.Store = "sqlite://" + filepath.Join(state, "store.db")
	case c.Store == "memory:" || c.Store == "memory":
		c.Store = "memory:"
	case strings.HasPrefix(c.Store, "sqlite://"):
		c.Store = "sqlite://" + expand(strings.TrimPrefix(c.Store, "sqlite://"))
	default:
		return fmt.Errorf("store %q: sqlite:///path or memory", c.Store)
	}
	if c.Retention == 0 {
		c.Retention = Duration(168 * time.Hour)
	}
	l := &c.Limits
	def := func(v *Size, d Size) {
		if *v == 0 {
			*v = d
		}
	}
	defInt := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	def(&l.MaxBody, 1<<20)
	def(&l.MaxReply, 1<<20)
	def(&l.MaxMeta, 4<<10)
	def(&l.MaxFilesSize, 16<<20)
	defInt(&l.MaxFiles, 16)
	if l.MaxFiles < 0 || l.MaxFiles > 256 {
		return fmt.Errorf("limits.max_files: 0..256")
	}
	def(&l.MaxStore, 2<<30)
	defInt(&l.MaxDepth, 4)
	defInt(&l.MaxQueuedPerAgent, 1000)
	defInt(&l.MaxEventsPerMessage, 1000)
	defInt(&l.MaxAttempts, 3)

	for i, t := range c.Tokens {
		if !ValidName(t.Name) {
			return fmt.Errorf("tokens[%d].name %q", i, t.Name)
		}
		switch {
		case t.SHA256 != "":
			h, err := hex.DecodeString(t.SHA256)
			if err != nil || len(h) != sha256.Size {
				return fmt.Errorf("tokens[%d].sha256: 64 hex digits", i)
			}
			t.hash = h
		case t.TokenEnv != "":
			v := os.Getenv(t.TokenEnv)
			if v == "" {
				return fmt.Errorf("tokens[%d].token_env %s is not set", i, t.TokenEnv)
			}
			h := sha256.Sum256([]byte(v))
			t.hash = h[:]
		default:
			return fmt.Errorf("tokens[%d]: sha256 or token_env", i)
		}
	}
	for name, a := range c.Agents {
		if !ValidName(name) {
			return fmt.Errorf("agents.%s: the name may have letters, digits, . _ -", name)
		}
		if a == nil {
			return fmt.Errorf("agents.%s: empty", name)
		}
		if err := a.fill(); err != nil {
			return fmt.Errorf("agents.%s: %w", name, err)
		}
	}
	return nil
}

func (a *Agent) fill() error {
	switch a.Driver {
	case "acp":
		if len(a.Command) == 0 || a.Command[0] == "" {
			return errors.New("driver acp needs a command")
		}
		a.Command[0] = expand(a.Command[0])
	case "inbox":
	default:
		return fmt.Errorf("driver %q: acp or inbox", a.Driver)
	}
	a.Cwd = expand(a.Cwd)
	for k, v := range a.Env {
		a.Env[k] = expand(v)
	}
	if a.Permissions == "" {
		a.Permissions = "allow"
	}
	if a.Permissions != "allow" && a.Permissions != "deny" {
		return fmt.Errorf("permissions %q: allow or deny", a.Permissions)
	}
	if a.Session == "" {
		a.Session = "task"
	}
	if a.Session != "task" && a.Session != "thread" && a.Session != "message" {
		return fmt.Errorf("session %q: task, thread or message", a.Session)
	}
	if a.Reply == "" {
		a.Reply = "last"
	}
	if a.Reply != "last" && a.Reply != "all" {
		return fmt.Errorf("reply %q: last or all", a.Reply)
	}
	if a.Concurrency <= 0 {
		a.Concurrency = 1
	}
	if a.TurnTimeout == 0 {
		a.TurnTimeout = Duration(30 * time.Minute)
	}
	if a.IdleTimeout == 0 {
		a.IdleTimeout = Duration(10 * time.Minute)
	}
	if a.Lease == 0 {
		a.Lease = Duration(5 * time.Minute)
	}
	if len(a.AllowFrom) == 0 {
		a.AllowFrom = []string{"*"}
	}
	return nil
}

// Duration is a time.Duration written as "90s", "30m", "168h".
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error { return d.parse(n.Value) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return d.parse(s)
}

func (d *Duration) parse(s string) error {
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || v < 0 {
		return fmt.Errorf("duration %q: like 90s, 30m, 168h", s)
	}
	*d = Duration(v)
	return nil
}

// Size is a number of bytes written as 1024, "64KiB", "1MiB", "2GiB" (or KB, MB, GB).
type Size int64

func (s *Size) UnmarshalYAML(n *yaml.Node) error { return s.parse(n.Value) }

func (s *Size) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	return s.parse(fmt.Sprint(v))
}

var units = []struct {
	suffix string
	mult   int64
}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"B", 1}}

func (s *Size) parse(in string) error {
	str := strings.TrimSpace(in)
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(str, u.suffix) {
			str, mult = strings.TrimSpace(strings.TrimSuffix(str, u.suffix)), u.mult
			break
		}
	}
	v, err := strconv.ParseInt(str, 10, 64)
	if err != nil || v < 0 {
		return fmt.Errorf("size %q: like 1024, 64KiB, 1MiB", in)
	}
	*s = Size(v * mult)
	return nil
}

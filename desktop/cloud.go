//go:build darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"gopkg.in/yaml.v3"
)

// The cloud in the app: connecting one (a key of an agent-sandbox tenant), and building its
// agents (the node's builder: the catalogue, a check, a deploy, a draft by an agent of this Mac).

// CloudInfo says which cloud the node is connected to, and as whom.
func (n *Node) CloudInfo() (client.CloudInfo, error) {
	return call(n, func(ctx context.Context, c *client.Client) (client.CloudInfo, error) { return c.CloudInfo(ctx) })
}

// CloudBuild is what an agent's definition is written from.
func (n *Node) CloudBuild() (client.CloudCatalogue, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) (client.CloudCatalogue, error) { return c.CloudBuild(ctx) })
}

// CloudPlanAgent has the cloud check a definition: what a deploy would change. Nothing is written.
func (n *Node) CloudPlanAgent(a client.CloudAgentSpec) (client.CloudPlan, error) {
	return callLong(n, 60*time.Second, func(ctx context.Context, c *client.Client) (client.CloudPlan, error) { return c.CloudPlanAgent(ctx, a) })
}

// CloudDeployAgent writes a definition into the catalogue as the check (plan) showed it.
func (n *Node) CloudDeployAgent(a client.CloudAgentSpec, plan string) (client.CloudDeployed, error) {
	return callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) (client.CloudDeployed, error) {
		return c.CloudDeployAgent(ctx, a, plan)
	})
}

// CloudDeleteAgent takes an agent out of the catalogue.
func (n *Node) CloudDeleteAgent(name string) error {
	_, err := call(n, func(ctx context.Context, c *client.Client) (struct{}, error) {
		return struct{}{}, c.CloudDeleteAgent(ctx, name)
	})
	return err
}

// CloudDraft starts an agent of this Mac writing a definition from the person's words.
func (n *Node) CloudDraft(r client.CloudDraftRequest) (client.CloudDraft, error) {
	return callLong(n, 90*time.Second, func(ctx context.Context, c *client.Client) (client.CloudDraft, error) { return c.CloudDraft(ctx, r) })
}

// cloudDraftWait is how long one CloudDraftResult waits for the draft to end.
var cloudDraftWait = 40 * time.Second

// CloudDraftResult waits a while for a draft and says what it came to: called again while its
// state is not final.
func (n *Node) CloudDraftResult(msg string) (client.CloudDraft, error) {
	return callLong(n, cloudDraftWait+30*time.Second, func(ctx context.Context, c *client.Client) (client.CloudDraft, error) {
		if _, err := c.Wait(ctx, msg, cloudDraftWait); err != nil {
			return client.CloudDraft{}, err
		}
		return c.CloudDraftResult(ctx, msg)
	})
}

// CloudConnected is a cloud just connected: who the key is, and whether the node runs with it
// already (the app's own node was started again) or has to be restarted by its owner.
type CloudConnected struct {
	Info      client.CloudInfo `json:"info"`
	Restarted bool             `json:"restarted"`
}

// cloudEnvName is the file the app keeps the cloud's address and key in, beside the configuration.
const cloudEnvName = "cloud.env"

// CloudConnect connects the node to a cloud: the key is asked about first (a key the cloud
// refuses is kept nowhere), then written to a file of the user's alone and named in the node's
// configuration, and the app's own node is started again with it.
func (n *Node) CloudConnect(rawURL, token, ca string) (CloudConnected, error) {
	var out CloudConnected
	if os.Getenv("ACPOST_NODE") != "" {
		return out, errors.New("the app is on another node (ACPOST_NODE): the cloud is set in that node's configuration, as cloud:")
	}
	c := &config.Cloud{URL: strings.TrimRight(strings.TrimSpace(rawURL), "/"), Token: strings.TrimSpace(token), CA: strings.TrimSpace(ca)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	info, err := node.CheckCloud(ctx, c)
	cancel()
	if err != nil {
		return out, nodeErr(err)
	}
	out.Info = info
	path := config.DefaultPath()
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := writeDefaultConfig(path); err != nil {
			return out, fmt.Errorf("write %s: %w", path, err)
		}
	}
	env := filepath.Join(filepath.Dir(path), cloudEnvName)
	if err := writeCloud(path, env, c); err != nil {
		return out, err
	}
	out.Restarted, err = n.restartNode()
	return out, err
}

// nodeErr hands the interface a node's error as withHint does a client's: code, message, hint.
func nodeErr(err error) error {
	var e *node.Error
	if errors.As(err, &e) {
		b, _ := json.Marshal(map[string]string{"code": e.Code, "message": e.Message, "hint": e.Hint})
		return errors.New(string(b))
	}
	return err
}

// writeCloud writes the key's file, then names it in the configuration: the configuration is
// changed only where it says cloud (the rest, its comments too, stays), and only to one the node
// can read. A configuration that fails leaves the key's file as it was.
func writeCloud(path, env string, c *config.Cloud) error {
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".yaml" && ext != ".yml" {
		return fmt.Errorf("%s is not YAML: set cloud: {env_file: …} in it by hand", path)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	next, err := setCloudYAML(old, env, c.CA)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	prev, prevErr := os.ReadFile(env)
	body := "# Written by the acpost app: the cloud's address and key (as sandboxctl reads them).\nSANDBOXD_URL=" + c.URL + "\nSANDBOXD_TOKEN=" + c.Token + "\n"
	if err := writeAtomic(env, []byte(body), 0o600, nil, true); err != nil {
		return err
	}
	undo := func() {
		if prevErr == nil {
			_ = writeAtomic(env, prev, 0o600, nil, true)
		} else {
			_ = os.Remove(env)
		}
	}
	// Read as the node will read it, before it replaces what works now.
	if _, err := config.Parse(next, ".yaml"); err != nil {
		undo()
		return fmt.Errorf("%s with the cloud: %w", path, err)
	}
	mode := fi.Mode().Perm()
	if err := writeAtomic(path, next, 0o600, &mode, true); err != nil {
		undo()
		return err
	}
	return nil
}

// setCloudYAML is a configuration with its cloud set to the env file (and the CA, when one is
// named): the cloud's own value replaced, or added at the end.
func setCloudYAML(src []byte, env, ca string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	val := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	add := func(k, v string) {
		val.Content = append(val.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Value: v, Style: yaml.DoubleQuotedStyle})
	}
	add("env_file", env)
	if ca != "" {
		add("ca", ca)
	}
	if doc.Kind == 0 { // an empty file
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping at the top")
	}
	top := doc.Content[0]
	set := false
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "cloud" {
			top.Content[i+1] = val
			set = true
		}
	}
	if !set {
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "cloud"}, val)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// nodeRestartWait is how long the app's own node has to stop before it is started again.
var nodeRestartWait = 20 * time.Second

// restartNode starts the app's own node again (a changed configuration): true when it did, false
// when the node that serves is not the app's (its owner restarts it). Turns running in it end as
// a node's stop ends them.
func (n *Node) restartNode() (bool, error) {
	n.startMu.Lock()
	defer n.startMu.Unlock()
	if n.ownNode() != nodeUp {
		return false, nil
	}
	n.mu.Lock()
	stop, done := n.stopNode, n.nodeDone
	n.mu.Unlock()
	stop()
	select {
	case <-done:
	case <-time.After(nodeRestartWait):
		return false, fmt.Errorf("the node did not stop in %s: quit the app and open it again", nodeRestartWait)
	}
	n.mu.Lock()
	if n.nodeDone == done {
		n.nodeDone, n.stopNode, n.nodeOK = nil, nil, false
	}
	n.c = nil
	n.mu.Unlock()
	err := n.startNode()
	n.mu.Lock()
	n.startErr = ""
	if err != nil {
		n.startErr = err.Error()
	}
	n.mu.Unlock()
	return err == nil, err
}

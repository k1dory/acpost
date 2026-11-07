package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
	"github.com/k1dory/acpx"
)

// A task's directory, and the model, reasoning level and mode a message asks for: meta keys only
// acpost sets (from SendRequest.Cwd, .Model, .Effort, .Mode), for an ACP agent of this node.
const (
	metaCwd    = "acp_cwd"
	metaModel  = "acp_model"
	metaEffort = "acp_thought_level"
	metaMode   = "acp_mode"
	// metaModeStrict: the mode is a condition, not a wish — a turn that cannot set it does not run
	// (the wiki's read-only mode).
	metaModeStrict = "acp_mode_strict"
)

// taskCwd is the directory a message's session runs in: the session it continues, else the one its
// task was started in ("" for the agent's own).
func taskCwd(m store.Message) string {
	if c := m.Meta[metaSessionCwd]; c != "" {
		return c
	}
	return m.Meta[metaCwd]
}

// resolveSettings checks a message's directory and settings and puts them in its meta. Only the
// node's user and its agents choose them; a message in a thread inherits the thread's directory.
func (n *Node) resolveSettings(ctx context.Context, p Principal, to Address, r *SendRequest) error {
	// A node forwards acp_ keys its node set; these are this node's to set, from its user's choice.
	for _, k := range []string{metaCwd, metaModel, metaEffort, metaMode, metaModeStrict} {
		delete(r.Meta, k)
	}
	asked := r.Cwd != "" || r.Model != "" || r.Effort != "" || r.Mode != ""
	if !asked && r.Thread == "" {
		return nil
	}
	cfg := n.agentCfg(to.Name)
	local := cfg != nil && cfg.Driver == "acp" && (to.Node == "" || to.Node == n.Name())
	if asked {
		if !p.local() {
			return errf("refused", "the directory, model, effort and mode are chosen by the node's user")
		}
		if !local {
			return errh("invalid", "they are an ACP agent's of this node", "%s is not an ACP agent of this node", to)
		}
	}
	set := func(k, v string) {
		if v == "" {
			return
		}
		if r.Meta == nil {
			r.Meta = map[string]string{}
		}
		r.Meta[k] = v
	}
	for k, v := range map[string]string{metaModel: r.Model, metaEffort: r.Effort, metaMode: r.Mode} {
		if len(v) > 128 || strings.ContainsFunc(v, func(c rune) bool { return c < ' ' }) {
			return errf("invalid", "%s: up to 128 printable characters", strings.TrimPrefix(k, "acp_"))
		}
		set(k, v)
	}
	if r.ModeStrict && r.Mode != "" {
		set(metaModeStrict, "1")
	}
	switch {
	case r.Cwd != "":
		if r.Session != "" {
			return errf("invalid", "a session runs in its own directory: no cwd with --session")
		}
		dir, err := checkDir(r.Cwd)
		if err != nil {
			return err
		}
		if r.Thread != "" {
			if root, err := n.st.Get(ctx, r.Thread); err == nil && root.Meta[metaCwd] != "" && root.Meta[metaCwd] != dir {
				return errf("invalid", "the thread runs in %s: a new thread for another directory", root.Meta[metaCwd])
			}
		}
		set(metaCwd, dir)
	case r.Thread != "" && local && r.Session == "":
		// The thread's directory, as its first message chose it.
		if root, err := n.st.Get(ctx, r.Thread); err == nil && root.To == "agent:"+to.Name {
			set(metaCwd, root.Meta[metaCwd])
		}
	}
	return nil
}

// checkDir: an absolute path to a directory that exists, cleaned.
func checkDir(dir string) (string, error) {
	if strings.HasPrefix(dir, "~/") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		return "", errf("invalid", "cwd %q: an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", errf("invalid", "cwd %s: not a directory here", dir)
	}
	return dir, nil
}

// applySettings selects what the message asks for in its session, before its prompt. What the
// agent does not offer is said in a notice; the turn goes on with the session as it is.
// applySettings sets the message's mode, model and effort in the session; an error when a strict
// mode could not be set (the turn must not run).
func (s *acpSlot) applySettings(ctx context.Context, p *acpProc, sid acp.SessionId, m store.Message) error {
	st := p.states[sid]
	changed := false
	for _, set := range []struct {
		s    acpx.Setting
		want string
	}{{acpx.ModeSetting, m.Meta[metaMode]}, {acpx.ModelSetting, m.Meta[metaModel]}, {acpx.ThoughtLevelSetting, m.Meta[metaEffort]}} {
		if set.want == "" {
			continue
		}
		next, err := acpx.Configure(ctx, p.conn, sid, st, set.s, set.want)
		if err != nil && set.s.Name == acpx.ModeSetting.Name && m.Meta[metaModeStrict] == "1" {
			return fmt.Errorf("the mode %s could not be set: %v", set.want, err)
		}
		if err != nil {
			if eventErr := s.a.n.addEvent(s.a.n.ctx, m.ID, "notice", eventJSON(map[string]string{"severity": "warning",
				"text": set.s.Name + " " + set.want + ": " + err.Error()}), true); eventErr != nil {
				s.a.n.log.Debug("event", "err", eventErr)
			}
			continue
		}
		st, changed = next, true
	}
	if changed {
		s.a.noteState(p, sid, st)
	}
	return nil
}

// noteState keeps a session's state: its model for usage, the agent's offer for the app.
func (a *acpAgent) noteState(p *acpProc, sid acp.SessionId, st acpx.SessionState) {
	if p.states == nil {
		p.states = map[acp.SessionId]acpx.SessionState{}
	}
	p.states[sid] = st
	p.setModel(sid, st.Options)
	a.optMu.Lock()
	a.lastOpts = &st
	a.optMu.Unlock()
}

func (a *acpAgent) noteCommands(cmds []acp.AvailableCommand) {
	a.optMu.Lock()
	a.commands = append([]acp.AvailableCommand(nil), cmds...)
	a.optMu.Unlock()
}

// offer is the agent's latest session's options; when no session said them yet, a session is opened
// to ask, in a process of its own (one asking at a time).
func (a *acpAgent) offer(ctx context.Context) (*acpx.SessionState, error) {
	a.optAsk.Lock()
	defer a.optAsk.Unlock()
	a.optMu.Lock()
	st := a.lastOpts
	a.optMu.Unlock()
	if st != nil {
		return st, nil
	}
	slot, done := a.spare()
	defer done()
	proc, err := slot.ensureProc(ctx)
	if err != nil {
		return nil, errf("unavailable", "agent:%s did not start: %v", a.name, err)
	}
	r, err := proc.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: slot.cwd(), McpServers: a.mcpServers(ctx, proc)})
	if err != nil {
		return nil, errf("unavailable", "agent:%s: session/new: %v", a.name, procErr(proc, err))
	}
	a.noteState(proc, r.SessionId, acpx.SessionState{Modes: r.Modes, Options: r.ConfigOptions})
	time.Sleep(100 * time.Millisecond) // the commands the agent says right after session/new
	slot.closeIfOneOff(proc, "", r.SessionId)
	a.optMu.Lock()
	defer a.optMu.Unlock()
	return a.lastOpts, nil
}

// AgentOptions is what an ACP agent offers to choose: its models, reasoning levels and modes, as
// its latest session said (a session is opened in the agent's directory to ask, when none was),
// and its slash commands.
func (n *Node) AgentOptions(ctx context.Context, p Principal, agent string) (client.AgentOptions, error) {
	if !p.local() {
		return client.AgentOptions{}, errf("refused", "an agent's options are for the node's user")
	}
	a := n.acp[agent]
	if a == nil {
		return client.AgentOptions{}, errh("not_found", "`acpost agents` lists them", "no ACP agent %s here", agent)
	}
	st, err := a.offer(ctx)
	if err != nil {
		return client.AgentOptions{}, err
	}
	out := client.AgentOptions{Agent: agent}
	for _, o := range st.Options {
		sel := o.Select
		if sel == nil {
			continue
		}
		opt := client.AgentOption{ID: string(sel.Id), Name: sel.Name, Current: string(sel.CurrentValue)}
		if sel.Category != nil {
			opt.Category = string(*sel.Category)
		}
		add := func(v acp.SessionConfigSelectOption, group string) {
			c := client.OptionValue{Value: string(v.Value), Name: v.Name, Group: group}
			if v.Description != nil {
				c.Description = *v.Description
			}
			opt.Values = append(opt.Values, c)
		}
		if u := sel.Options.Ungrouped; u != nil {
			for _, v := range *u {
				add(v, "")
			}
		}
		if g := sel.Options.Grouped; g != nil {
			for _, grp := range *g {
				for _, v := range grp.Options {
					add(v, grp.Name)
				}
			}
		}
		out.Options = append(out.Options, opt)
	}
	if st.Modes != nil && acpx.ConfigOption(st.Options, acp.SessionConfigOptionCategoryMode) == nil {
		opt := client.AgentOption{ID: "mode", Name: "Mode", Category: "mode", Current: string(st.Modes.CurrentModeId)}
		for _, m := range st.Modes.AvailableModes {
			v := client.OptionValue{Value: string(m.Id), Name: m.Name}
			if m.Description != nil {
				v.Description = *m.Description
			}
			opt.Values = append(opt.Values, v)
		}
		out.Options = append(out.Options, opt)
	}
	a.optMu.Lock()
	for _, c := range a.commands {
		out.Commands = append(out.Commands, client.AgentCommand{Name: c.Name, Description: c.Description})
	}
	a.optMu.Unlock()
	return out, nil
}

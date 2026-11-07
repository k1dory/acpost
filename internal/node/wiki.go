package node

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The wiki of a repository: one of this node's ACP agents reads the directory (in its read-only
// mode, so nothing waits for a person) and answers with pages in a plain format; the node keeps
// them. Writing one is an ordinary task in a thread of its own, which the app can open.

const wikiMaxPages = 40

var (
	wikiPage = regexp.MustCompile(`(?s)<<<PAGE\s+slug="([a-z0-9][a-z0-9-]{0,63})"\s+title="([^"\n]{1,120})"(?:\s+parent="([a-z0-9-]{0,64})")?\s*>>>\n(.*?)\n<<<END>>>`)
	// The modes that read and change nothing: Claude Code's plan, Codex's read-only.
	readOnly = regexp.MustCompile(`(?i)^(plan|plan mode|read[-_ ]?only)$`)
)

func wikiPrompt(lang string) string {
	in := "English"
	if lang == "ru" {
		in = "Russian"
	}
	return `Write a wiki for the repository in your working directory, for a developer new to it. Read the code first: the layout, the entry points, the main packages or modules, how data flows, how it is built, tested and run. Change nothing.

Then answer with the pages and nothing else, each in exactly this form:

<<<PAGE slug="overview" title="Overview">>>
(Markdown)
<<<END>>>

Rules:
- 5 to 12 pages. The first is the overview: what it is, who uses it, the main parts. Then the architecture (with a Mermaid diagram in a ` + "```mermaid" + ` block), one page per important module or subsystem (parent="architecture" for those), data and storage, how to build, test and run, configuration, and a glossary when the domain has its own words.
- slug: lowercase letters, digits and dashes; title: a short name. Link pages to each other as [[slug]].
- Mermaid: valid syntax only (flowchart TD or LR, sequenceDiagram); put every node label and edge label in double quotes — A["cmd/acpost (CLI)"] -->|"HTTP /v1"| B["node"] — no HTML in labels, at most about 15 nodes.
- Be concrete: name files as path/to/file.go:123, show real commands, say why things are as they are when the code or its comments tell. No filler, no guessing — what you are unsure of, leave out.
- Do not repeat the page's title as a heading at its top.
- Write the pages in ` + in + `.`
}

// parseWiki takes the pages out of an agent's answer.
func parseWiki(reply string) []store.WikiPage {
	var out []store.WikiPage
	seen := map[string]bool{}
	for _, m := range wikiPage.FindAllStringSubmatch(reply, -1) {
		if seen[m[1]] || len(out) >= wikiMaxPages {
			continue
		}
		seen[m[1]] = true
		out = append(out, store.WikiPage{Slug: m[1], Title: strings.TrimSpace(m[2]), Parent: m[3], Body: strings.TrimSpace(m[4])})
	}
	// Two levels: a parent is a page at the top (not one, or a page with a parent of its own —
	// which also breaks cycles — makes the page a top one).
	parentOf := map[string]string{}
	for _, p := range out {
		parentOf[p.Slug] = p.Parent
	}
	for i := range out {
		par := out[i].Parent
		if par != "" && (!seen[par] || par == out[i].Slug || parentOf[par] != "") {
			out[i].Parent = ""
		}
	}
	return out
}

// Wikis lists the wikis (those being written whose task ended are taken in first).
func (n *Node) Wikis(ctx context.Context, p Principal) ([]store.Wiki, error) {
	if err := userOnly(p, "the wiki"); err != nil {
		return nil, err
	}
	list, err := n.st.Wikis(ctx)
	if err != nil {
		return nil, errf("unavailable", "%v", err)
	}
	changed := false
	for _, w := range list {
		if w.Status == "writing" && n.takeWiki(ctx, w) {
			changed = true
		}
	}
	if changed {
		if list, err = n.st.Wikis(ctx); err != nil {
			return nil, errf("unavailable", "%v", err)
		}
	}
	return list, nil
}

// WikiPages is a wiki's pages, with their text.
func (n *Node) WikiPages(ctx context.Context, p Principal, repo string) ([]store.WikiPage, error) {
	if err := userOnly(p, "the wiki"); err != nil {
		return nil, err
	}
	out, err := n.st.WikiPages(ctx, repo, true)
	if err != nil {
		return nil, errf("unavailable", "%v", err)
	}
	return out, nil
}

// WriteWiki has an agent of this node write (or write again) the wiki of a directory: the
// message's id, whose thread shows the work.
func (n *Node) WriteWiki(ctx context.Context, p Principal, repo, agent, lang string) (store.Message, error) {
	if err := userOnly(p, "the wiki"); err != nil {
		return store.Message{}, err
	}
	dir, err := checkDir(repo)
	if err != nil {
		return store.Message{}, err
	}
	to, err := ParseAddress(agent)
	if err != nil || to.Kind != "agent" || to.Node != "" || n.acp[to.Name] == nil {
		return store.Message{}, errf("invalid", "%q: an ACP agent of this node (agent:claude, agent:codex)", agent)
	}
	if lang != "ru" {
		lang = "en"
	}
	// Its read-only mode: reading a repository then asks nobody and changes nothing. No such mode,
	// no wiki (a turn that cannot set it fails before it reads anything).
	mode := ""
	if opts, err := n.AgentOptions(ctx, p, to.Name); err == nil {
		for _, o := range opts.Options {
			if o.Category != "mode" {
				continue
			}
			for _, v := range o.Values {
				if readOnly.MatchString(v.Value) || readOnly.MatchString(v.Name) {
					mode = v.Value
					break
				}
			}
		}
	}
	if mode == "" {
		return store.Message{}, errh("unsupported", "choose an agent with a plan or read-only mode (Claude Code, Codex)",
			"%s has no read-only mode: it would ask to run commands while it reads", to)
	}
	// The wiki is taken first (one writer at a time), then the task is sent.
	pending := "pending-" + store.NewID()
	ok, err := n.st.StartWiki(ctx, dir, to.String(), lang, pending)
	if err != nil {
		return store.Message{}, errf("unavailable", "%v", err)
	}
	if !ok {
		return store.Message{}, errf("conflict", "the wiki of %s is being written already", dir)
	}
	m, _, err := n.Send(ctx, p, SendRequest{To: to.String(), Body: wikiPrompt(lang), Cwd: dir, Mode: mode, ModeStrict: true,
		Meta: map[string]string{"wiki": "1"}}) // a mark (meta is small): the wiki keeps the task, not the task the wiki
	if err != nil {
		_ = n.st.FinishWiki(context.WithoutCancel(ctx), dir, pending, nil, "the task could not be sent: "+err.Error())
		return m, err
	}
	if err := n.st.SetWikiMsg(context.WithoutCancel(ctx), dir, pending, m.ID); err != nil {
		return m, errf("unavailable", "%v", err)
	}
	w := store.Wiki{Repo: dir, Msg: m.ID, Status: "writing"}
	n.goWork(func() { n.followWiki(w) })
	return m, nil
}

// DeleteWiki forgets a wiki.
func (n *Node) DeleteWiki(ctx context.Context, p Principal, repo string) error {
	if err := userOnly(p, "the wiki"); err != nil {
		return err
	}
	if err := n.st.DeleteWiki(ctx, repo); errors.Is(err, store.ErrNoWiki) {
		return errf("not_found", "%v", err)
	} else if err != nil {
		return errf("unavailable", "%v", err)
	}
	return nil
}

// followWiki waits for the task writing a wiki to end, then takes its pages in.
func (n *Node) followWiki(w store.Wiki) {
	me := Principal{Kind: "user", Name: n.login}
	for n.ctx.Err() == nil {
		m, err := n.Wait(n.ctx, me, w.Msg, 10*time.Minute)
		if err == nil && !m.State.Final() {
			continue
		}
		n.takeWiki(n.ctx, w)
		return
	}
}

// takeWiki takes in the pages of a wiki's task once it ended: true when it did (or failed).
func (n *Node) takeWiki(ctx context.Context, w store.Wiki) bool {
	if strings.HasPrefix(w.Msg, "pending-") {
		// Taken, and the node stopped before its task was sent (or noted): after a minute, over.
		if time.Since(w.Updated) > time.Minute {
			_ = n.st.FinishWiki(ctx, w.Repo, w.Msg, nil, "the task writing it was never sent")
			return true
		}
		return false
	}
	m, err := n.st.Get(ctx, w.Msg)
	switch {
	case errors.Is(err, store.ErrNotFound):
		_ = n.st.FinishWiki(ctx, w.Repo, w.Msg, nil, "the task writing it is gone")
		return true
	case err != nil || !m.State.Final():
		return false
	case m.State != store.Done:
		_ = n.st.FinishWiki(ctx, w.Repo, w.Msg, nil, fmt.Sprintf("the agent did not finish it (%s): %s", m.State, cmpOr(m.Error, m.ErrorCode)))
		return true
	}
	pages := parseWiki(m.Reply)
	if len(pages) == 0 {
		_ = n.st.FinishWiki(ctx, w.Repo, w.Msg, nil, "the agent's answer had no pages in the wiki's format")
		return true
	}
	_ = n.st.FinishWiki(ctx, w.Repo, w.Msg, pages, "")
	return true
}

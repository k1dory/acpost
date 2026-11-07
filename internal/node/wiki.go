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
	readOnly = regexp.MustCompile(`(?i)plan|read`)
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
- slug: lowercase letters, digits and dashes; title: a short name.
- Be concrete: name files as path/to/file.go:123, show real commands, say why things are as they are when the code or its comments tell. No filler, no guessing — what you are unsure of, leave out.
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
	for i := range out { // a parent that is not a page: none
		if out[i].Parent != "" && (!seen[out[i].Parent] || out[i].Parent == out[i].Slug) {
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
	// Its read-only mode, when it has one: reading a repository then asks nobody.
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
	m, _, err := n.Send(ctx, p, SendRequest{To: to.String(), Body: wikiPrompt(lang), Cwd: dir, Mode: mode,
		Meta: map[string]string{"wiki": dir}})
	if err != nil {
		return m, err
	}
	ok, err := n.st.StartWiki(ctx, dir, to.String(), lang, m.ID)
	if err != nil || !ok {
		_, _ = n.Cancel(context.WithoutCancel(ctx), p, m.ID)
		if err != nil {
			return m, errf("unavailable", "%v", err)
		}
		return m, errf("conflict", "the wiki of %s is being written already", dir)
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

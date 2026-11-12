package node

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// Chats: everything the node's user talks with, in one list for the app — acpost's threads, the
// ACP sessions of this node's agents started anywhere (Claude Code, Codex in a terminal or an
// editor), cloud sandboxes — the most recently active first, each with its folder.

const (
	chatSessionsPerAgent = 300              // newest sessions of an agent shown (not counting acpost's own threads')
	chatThreads          = 500              // newest threads listed
	chatListWait         = 20 * time.Second // for an agent to list its sessions
)

// Chats lists the node's user's chats.
func (n *Node) Chats(ctx context.Context, p Principal) (client.Chats, error) {
	if err := userOnly(p, "chats"); err != nil {
		return client.Chats{}, err
	}
	var out client.Chats
	folders, err := n.st.Folders(ctx)
	if err != nil {
		return out, errf("unavailable", "%v", err)
	}
	folderOf := map[string]string{}
	for _, f := range folders {
		for _, c := range f.Chats {
			folderOf[c] = f.ID
		}
	}

	// acpost's threads, and the ACP sessions they run (not listed again below).
	ours := map[string]bool{} // agent name + "/" + session id
	before := ""
	for len(out.Items) < chatThreads {
		convs, next, err := n.st.Conversations(ctx, nil, before, 200)
		if err != nil {
			return out, errf("unavailable", "%v", err)
		}
		for _, c := range convs {
			chat := client.Chat{Key: "thread:" + c.Thread, Kind: "thread", Thread: c.Thread, Title: c.Title,
				Updated: c.Updated, Running: c.Running, Queued: c.Queued, Failed: c.Failed, Messages: c.Messages}
			if len(c.Agents) > 0 {
				chat.Agent = c.Agents[0]
			}
			root, rootErr := n.st.Get(ctx, c.Thread)
			if rootErr == nil {
				chat.Cwd = taskCwd(root)
				// A chat of several agents is its first one's (the list's order is the store's).
				if slices.Contains(c.Agents, root.To) {
					chat.Agent = root.To
				}
			}
			if len(c.Agents) > 1 {
				chat.Agents = c.Agents
				sort.Strings(chat.Agents)
			}
			// The ACP sessions this thread runs: agent by agent, under the key its turns use.
			inherit := ""
			for _, a := range c.Agents {
				name := strings.TrimPrefix(a, "agent:")
				if strings.HasPrefix(name, "sb_") {
					ours["cloud/"+name] = true // a cloud sandbox this thread talks to: the thread is its chat
					inherit = cmpOr(inherit, folderOf["cloud:"+name])
					continue
				}
				cfg := n.agentCfg(name)
				if strings.Contains(name, "@") || cfg == nil || rootErr != nil {
					continue
				}
				sid := ""
				if root.To == a {
					sid = root.Meta[metaSession] // a session it continues
				}
				if sid == "" {
					// The key this agent's turns of the thread use: the root's session is another agent's.
					r := root
					if root.To != a {
						r.Meta = nil
					}
					if key := sessionKey(cfg.Session, r); key != "" {
						sid, _ = n.st.Session(ctx, a, key)
					}
				}
				if sid != "" {
					ours[name+"/"+sid] = true
					if chat.Session == "" {
						chat.Session, chat.SessionAgent = sid, a
					}
					inherit = cmpOr(inherit, folderOf["session:"+name+"/"+sid])
				}
			}
			// A session or sandbox put in a folder keeps it once a thread continues it.
			chat.Folder = cmpOr(folderOf[chat.Key], inherit)
			out.Items = append(out.Items, chat)
		}
		if next == "" {
			break
		}
		before = next
	}

	// Sessions and sandboxes elsewhere: from the cache, refreshed apart once stale.
	ext, partial := n.externalChats(ctx, ours)
	out.Partial = append(out.Partial, partial...)
	shown := map[string]int{} // sessions shown per agent
	for _, c := range ext {
		if c.Kind == "session" {
			if shown[c.Agent] >= chatSessionsPerAgent || ours[strings.TrimPrefix(c.Agent, "agent:")+"/"+c.Session] {
				continue
			}
			shown[c.Agent]++
		}
		if c.Kind == "cloud" && ours["cloud/"+c.Sandbox] {
			continue
		}
		c.Folder = folderOf[c.Key]
		out.Items = append(out.Items, c)
	}

	// The names the user gave chats.
	if titles, err := n.st.ChatTitles(ctx); err == nil && len(titles) > 0 {
		for i := range out.Items {
			if t := titles[out.Items[i].Key]; t != "" {
				out.Items[i].Title = t
			}
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Updated.After(out.Items[j].Updated) })
	out.Folders = folders
	if out.Items == nil {
		out.Items = []client.Chat{}
	}
	return out, nil
}

// madeSession marks, in the store's session keys (a namespace of its own, like the sandbox
// cursors'), a session the node made for a turn of a thread or a task. It goes with the sessions
// unused past retention.
const madeSession = "made:"

// chatCacheFresh: how long the sessions and sandboxes listed elsewhere are shown without asking
// again; past it they are still shown while a refresh runs apart.
const chatCacheFresh = 30 * time.Second

// chatCache holds what the agents and the cloud last listed: asking them takes seconds.
type chatCache struct {
	mu         sync.Mutex
	at         time.Time
	items      []client.Chat
	partial    []string
	refreshing bool
	gen        int             // bumped by forget: a refresh begun before it does not count as fresh
	ours       map[string]bool // the threads' sessions the last caller gave
}

// externalChats: the ACP sessions of this node's agents and the cloud's sandboxes — the cached
// list (refreshed apart if stale), or, the first time, listed now.
//
// ours are the sessions acpost's threads run (agent name + "/" + id), as the caller found them:
// they are listed on top of the agent's limit, not in it (the refresh uses the latest given).
func (n *Node) externalChats(ctx context.Context, ours map[string]bool) ([]client.Chat, []string) {
	c := &n.chatList
	c.mu.Lock()
	c.ours = ours
	if c.at.IsZero() {
		c.mu.Unlock()
		items, partial := n.listExternal(ctx, ours)
		c.mu.Lock()
		if c.at.IsZero() {
			c.items, c.partial, c.at = items, partial, time.Now()
		}
		c.mu.Unlock()
		return items, partial
	}
	items, partial := append([]client.Chat(nil), c.items...), append([]string(nil), c.partial...)
	if time.Since(c.at) > chatCacheFresh && !c.refreshing {
		c.refreshing = true
		gen, owned := c.gen, c.ours
		started := n.goWork(func() {
			got, gotPartial := n.listExternal(n.ctx, owned)
			c.mu.Lock()
			defer c.mu.Unlock()
			c.refreshing = false
			if n.ctx.Err() != nil {
				return
			}
			c.items, c.partial = got, gotPartial
			if gen == c.gen {
				c.at = time.Now()
			} else {
				c.at = time.Now().Add(-2 * chatCacheFresh) // forgotten meanwhile: stale again
			}
		})
		if !started {
			c.refreshing = false
		}
	}
	c.mu.Unlock()
	return items, partial
}

// forgetChats marks the cached list stale (a session forked, a sandbox made): the next look
// refreshes it.
func (n *Node) forgetChats() {
	n.chatList.mu.Lock()
	if !n.chatList.at.IsZero() {
		n.chatList.at = time.Now().Add(-2 * chatCacheFresh)
	}
	n.chatList.gen++
	n.chatList.mu.Unlock()
}

// listExternal asks each ACP agent for its sessions, at once — up to chatSessionsPerAgent not among
// ours, and those of ours met on the way — and the cloud for its sandboxes.
func (n *Node) listExternal(ctx context.Context, ours map[string]bool) ([]client.Chat, []string) {
	p := Principal{Kind: "user", Name: n.login}
	var out []client.Chat
	var partial []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, a := range n.acp {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lctx, cancel := context.WithTimeout(ctx, chatListWait)
			defer cancel()
			var got []client.Chat
			cursor, others := "", 0
			// One process for every page: a spare one (no slot's is up) is started once a
			// listing, not once a page.
			fail := func(err error) {
				mu.Lock()
				partial = append(partial, "agent:"+name+": "+err.Error())
				mu.Unlock()
			}
			if err := canUseSessions(p, name, a.cfg); err != nil {
				fail(err)
				return
			}
			proc, done, err := a.listProc(lctx)
			defer done()
			if err != nil {
				fail(errors.New(explainStart(err).Message))
				return
			}
			made, err := n.st.SessionsMarked(lctx, a.addr, madeSession)
			if err != nil {
				fail(err)
				return
			}
			inUse := map[string]bool{}
			for _, prefix := range []string{"thread:", "task:", "from:"} { // sessionKey's keys
				ids, err := n.st.SessionsMarked(lctx, a.addr, prefix)
				if err != nil {
					fail(err)
					return
				}
				for id := range ids {
					inUse[id] = true
				}
			}
			for others < chatSessionsPerAgent {
				list, next, err := sessionsOf(lctx, proc, name, "", cursor)
				if err != nil {
					if CodeOf(err) != "unsupported" {
						mu.Lock()
						partial = append(partial, "agent:"+name+": "+err.Error())
						mu.Unlock()
					}
					break
				}
				for _, si := range list {
					// A session this node made for a thread or a task, that no key runs any more
					// (its turn was cancelled or tried again, and the thread went on in another),
					// is no chat: not shown, and not one of the sessions the agent's limit counts.
					// One a key still runs stays: the threads list theirs, and one no thread
					// claims is a chat here. Anything the node did not make stays too.
					if made[si.ID] && !inUse[si.ID] {
						continue
					}
					c := client.Chat{Key: "session:" + name + "/" + si.ID, Kind: "session", Agent: a.addr, Session: si.ID,
						Title: si.Title, Cwd: si.Cwd}
					if t, err := time.Parse(time.RFC3339Nano, si.UpdatedAt); err == nil {
						c.Updated = t
					}
					if !ours[name+"/"+si.ID] {
						others++
					}
					got = append(got, c)
				}
				if next == "" {
					break
				}
				cursor = next
			}
			mu.Lock()
			out = append(out, got...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if cl, err := n.cloudChats(ctx); err != nil {
		partial = append(partial, "cloud: "+err.Error())
	} else {
		out = append(out, cl...)
	}
	return out, partial
}

// linkedChats are the chats a thread continues — each of its agents' session, each cloud sandbox it
// talks to: their folder is the thread's.
func (n *Node) linkedChats(ctx context.Context, thread string) []string {
	root, err := n.st.Get(ctx, thread)
	if err != nil {
		return nil
	}
	var page []store.Message
	for after := ""; ; {
		got, next, err := n.st.List(ctx, store.Filter{Thread: thread, After: after, Limit: 500})
		if err != nil {
			return nil
		}
		page = append(page, got...)
		if next == "" || next == after {
			break
		}
		after = next
	}
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	agents := map[string]bool{root.To: true}
	for _, m := range page {
		agents[m.To] = true
	}
	for to := range agents {
		name := strings.TrimPrefix(to, "agent:")
		switch {
		case strings.Contains(name, "@"):
		case strings.HasPrefix(name, "sb_"):
			add("cloud:" + name)
		default:
			cfg := n.agentCfg(name)
			if cfg == nil {
				continue
			}
			r := root
			if root.To == to {
				if sid := root.Meta[metaSession]; sid != "" {
					add("session:" + name + "/" + sid)
				}
			} else {
				r.Meta = nil
			}
			if key := sessionKey(cfg.Session, r); key != "" {
				if sid, _ := n.st.Session(ctx, to, key); sid != "" {
					add("session:" + name + "/" + sid)
				}
			}
		}
	}
	return out
}

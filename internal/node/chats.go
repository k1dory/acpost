package node

import (
	"context"
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
	chatSessionsPerAgent = 300              // newest sessions of an agent listed
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

	// Each ACP agent's sessions, at once.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, a := range n.acp {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lctx, cancel := context.WithTimeout(ctx, chatListWait)
			defer cancel()
			var got []client.Chat
			cursor := ""
			for len(got) < chatSessionsPerAgent {
				list, next, err := n.Sessions(lctx, p, name, "", cursor)
				if err != nil {
					if CodeOf(err) != "unsupported" {
						mu.Lock()
						out.Partial = append(out.Partial, "agent:"+name+": "+err.Error())
						mu.Unlock()
					}
					break
				}
				for _, si := range list {
					if ours[name+"/"+si.ID] {
						continue
					}
					c := client.Chat{Key: "session:" + name + "/" + si.ID, Kind: "session", Agent: a.addr, Session: si.ID,
						Title: si.Title, Cwd: si.Cwd}
					if t, err := time.Parse(time.RFC3339Nano, si.UpdatedAt); err == nil {
						c.Updated = t
					}
					c.Folder = folderOf[c.Key]
					got = append(got, c)
				}
				if next == "" {
					break
				}
				cursor = next
			}
			mu.Lock()
			out.Items = append(out.Items, got...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Cloud sandboxes.
	if cl, err := n.cloudChats(ctx); err != nil {
		out.Partial = append(out.Partial, "cloud: "+err.Error())
	} else {
		for _, c := range cl {
			if ours["cloud/"+c.Sandbox] {
				continue
			}
			c.Folder = folderOf[c.Key]
			out.Items = append(out.Items, c)
		}
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

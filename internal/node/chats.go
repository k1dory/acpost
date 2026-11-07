package node

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/client"
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
			if root, err := n.st.Get(ctx, c.Thread); err == nil {
				chat.Cwd = taskCwd(root)
				if sid := root.Meta[metaSession]; sid != "" {
					chat.Session = sid
				}
			}
			for _, a := range c.Agents {
				name := strings.TrimPrefix(a, "agent:")
				if strings.Contains(name, "@") {
					continue
				}
				if sid, _ := n.st.Session(ctx, a, "thread:"+c.Thread); sid != "" {
					ours[name+"/"+sid] = true
					if chat.Session == "" {
						chat.Session = sid
					}
				}
				if chat.Session != "" {
					ours[name+"/"+chat.Session] = true
				}
			}
			chat.Folder = folderOf[chat.Key]
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
			c.Folder = folderOf[c.Key]
			out.Items = append(out.Items, c)
		}
	}

	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Updated.After(out.Items[j].Updated) })
	out.Folders = folders
	if out.Items == nil {
		out.Items = []client.Chat{}
	}
	return out, nil
}

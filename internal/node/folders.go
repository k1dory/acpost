package node

import (
	"context"
	"errors"
	"strings"

	"github.com/k1dory/acpost/internal/store"
)

// Folders of chats, the app's: the node's user's only.

func folderErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNoFolder):
		return errf("not_found", "%v", err)
	default:
		return errf("invalid", "%v", err)
	}
}

func userOnly(p Principal, what string) error {
	if p.Kind != "user" {
		return errf("refused", "%s are the node's user's", what)
	}
	return nil
}

// Folders lists the folders with their chats.
func (n *Node) Folders(ctx context.Context, p Principal) ([]store.Folder, error) {
	if err := userOnly(p, "folders"); err != nil {
		return nil, err
	}
	out, err := n.st.Folders(ctx)
	return out, folderErr(err)
}

// CreateFolder makes a folder.
func (n *Node) CreateFolder(ctx context.Context, p Principal, name string) (store.Folder, error) {
	if err := userOnly(p, "folders"); err != nil {
		return store.Folder{}, err
	}
	f, err := n.st.CreateFolder(ctx, name)
	return f, folderErr(err)
}

// UpdateFolder renames a folder or moves it in the order (pos < 0: where it is).
func (n *Node) UpdateFolder(ctx context.Context, p Principal, id, name string, pos int) error {
	if err := userOnly(p, "folders"); err != nil {
		return err
	}
	return folderErr(n.st.UpdateFolder(ctx, id, name, pos))
}

// DeleteFolder removes a folder (its chats are in none after it).
func (n *Node) DeleteFolder(ctx context.Context, p Principal, id string) error {
	if err := userOnly(p, "folders"); err != nil {
		return err
	}
	return folderErr(n.st.DeleteFolder(ctx, id))
}

// MoveChat puts a chat in a folder ("" takes it out).
func (n *Node) MoveChat(ctx context.Context, p Principal, chat, folder string) error {
	if err := userOnly(p, "folders"); err != nil {
		return err
	}
	var linked []string
	if thread, ok := strings.CutPrefix(chat, "thread:"); ok {
		linked = n.linkedChats(ctx, thread) // the chats the thread continues follow it
	}
	return folderErr(n.st.MoveChat(ctx, chat, folder, linked...))
}

// Playbooks lists the kept prompts.
func (n *Node) Playbooks(ctx context.Context, p Principal) ([]store.Playbook, error) {
	if err := userOnly(p, "playbooks"); err != nil {
		return nil, err
	}
	out, err := n.st.Playbooks(ctx)
	return out, folderErr(err)
}

// PutPlaybook keeps a prompt under a name.
func (n *Node) PutPlaybook(ctx context.Context, p Principal, pb store.Playbook) error {
	if err := userOnly(p, "playbooks"); err != nil {
		return err
	}
	return folderErr(n.st.PutPlaybook(ctx, pb))
}

// DeletePlaybook forgets a prompt.
func (n *Node) DeletePlaybook(ctx context.Context, p Principal, name string) error {
	if err := userOnly(p, "playbooks"); err != nil {
		return err
	}
	if err := n.st.DeletePlaybook(ctx, name); errors.Is(err, store.ErrNoPlaybook) {
		return errf("not_found", "%v", err)
	} else if err != nil {
		return errf("unavailable", "%v", err)
	}
	return nil
}

// SetChatTitle names a chat ("" gives it back its own).
func (n *Node) SetChatTitle(ctx context.Context, p Principal, chat, title string) error {
	if err := userOnly(p, "chats"); err != nil {
		return err
	}
	return folderErr(n.st.SetChatTitle(ctx, chat, title))
}

package node

import (
	"context"
	"errors"

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
	return folderErr(n.st.MoveChat(ctx, chat, folder))
}

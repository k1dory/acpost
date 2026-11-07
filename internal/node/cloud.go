package node

import (
	"context"

	"github.com/k1dory/acpost/internal/client"
)

// cloudChats lists the tenant's sandboxes as chats (none without a cloud configured).
func (n *Node) cloudChats(ctx context.Context) ([]client.Chat, error) {
	return nil, nil
}

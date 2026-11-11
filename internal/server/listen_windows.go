package server

import (
	"net"
	"path/filepath"

	"github.com/k1dory/acpost/internal/plat"
)

// listenPrivate listens on a Unix socket for its owner alone. Windows gives a socket's file no
// mode of its own to make it with: who may connect is its directory's access list, which the
// file inherits, so a directory open to others is refused before the socket is there.
func listenPrivate(path string) (net.Listener, error) {
	if err := plat.PrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return net.Listen("unix", path)
}

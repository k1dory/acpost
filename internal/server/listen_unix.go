//go:build unix

package server

import (
	"net"
	"syscall"
)

// listenPrivate listens on a Unix socket made for its owner alone.
func listenPrivate(path string) (net.Listener, error) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}

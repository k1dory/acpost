// Package server runs a node and its HTTP API on the configured listeners: what `acpost serve` and
// the desktop app run.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/k1dory/acpost/internal/api"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// Run runs a node and its API on cfg's listeners until ctx ends (or a listener fails, whose error
// it returns); ready, when set, is called once the node serves.
//
// Every listener is opened before any serves, and each resource is released as soon as a later
// step fails: a bind error on the second address leaves no first server running, no socket or pid
// file behind, and the next Run in the same process (the desktop's restart) can bind again. The
// API serves only once the node has started, so no request reaches a node half set up;
// connections made in between wait in the listen backlog.
func Run(ctx context.Context, cfg *config.Config, log *slog.Logger, ready func()) error {
	st, err := store.Open(cfg.Store, node.StoreOptions(cfg))
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := node.New(cfg, st, log)
	if err != nil {
		return err
	}
	// Deferred after st.Close, so it runs first: the drivers stop before their store closes.
	defer n.Close()

	// JSON may spell one byte of text in six (\u0000) and files take a third more in base64: a
	// request at the limits fits; the limits themselves are checked on the decoded content.
	maxReq := 6*max(int64(cfg.Limits.MaxBody), int64(cfg.Limits.MaxReply)) + 2*int64(cfg.Limits.MaxFilesSize) + 1<<20
	type listener struct {
		ln    net.Listener
		local bool
		addr  string
	}
	var lns []listener
	defer func() {
		for _, l := range lns {
			_ = l.ln.Close()
		}
	}()
	for _, l := range cfg.Node.Listen {
		if strings.HasPrefix(l, "unix://") {
			path := strings.TrimPrefix(l, "unix://")
			ln, err := ListenUnix(path)
			if err != nil {
				return err
			}
			lns = append(lns, listener{wrapListener(ln), true, l})
			pidFile := filepath.Join(filepath.Dir(path), "acpost.pid")
			defer func() { _ = os.Remove(path); _ = os.Remove(pidFile) }()
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
				return err
			}
			continue
		}
		var tc *tls.Config
		if t := cfg.Node.TLS; t != nil {
			cert, err := tls.LoadX509KeyPair(t.Cert, t.Key)
			if err != nil {
				return fmt.Errorf("node.tls: %w", err)
			}
			tc = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		}
		ln, err := net.Listen("tcp", strings.TrimPrefix(l, "tcp://"))
		if err != nil {
			return err
		}
		if tc != nil {
			ln = tls.NewListener(ln, tc)
		}
		lns = append(lns, listener{wrapListener(ln), false, l})
	}

	if err := n.Start(ctx); err != nil {
		return err
	}

	// Requests live in base: stopping ends it, so long polls (wait, claim, follow) return at once
	// and Shutdown does not sit out its grace on them. Their clients retry against the next node.
	// Only the stop ends it, and always as a cancellation (the API's unavailable), whatever ended
	// ctx: a deadline of Run's must not reach a handler as one of its own.
	base, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	errc := make(chan error, len(lns))
	var servers []*http.Server
	for _, l := range lns {
		srv := &http.Server{Handler: api.Handler(n, l.local, maxReq), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn), BaseContext: func(net.Listener) context.Context { return base }}
		servers = append(servers, srv)
		go func() {
			if err := srv.Serve(l.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", l.addr, err)
			}
		}()
		log.Info("listening", "on", l.addr)
	}
	log.Info("node up", "name", cfg.Node.Name, "agents", len(cfg.Agents), "store", cfg.Store)
	if ready != nil {
		ready()
	}

	var failed error
	select {
	case <-ctx.Done():
		log.Info("stopping")
	case failed = <-errc:
		log.Error("listener failed", "err", failed)
	}
	stop()
	sctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	for _, s := range servers {
		_ = s.Shutdown(sctx)
		_ = s.Close()
	}
	return failed
}

// wrapListener lets a test stand in for a listener that fails; Run serves what it returns.
var wrapListener = func(ln net.Listener) net.Listener { return ln }

// ListenUnix listens on a Unix socket in a private directory, replacing a stale socket file.
func ListenUnix(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return nil, fmt.Errorf("%s: a node is already listening there", path)
	}
	// Only a socket is stale; anything else at the path is someone's file, not ours to remove.
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s: exists and is not a socket", path)
		}
		_ = os.Remove(path)
	}
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	return ln, err
}

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

// Run runs a node and its API on cfg's listeners until ctx ends (or a listener fails); ready, when
// set, is called once the node serves.
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

	// JSON may spell one byte of text in six (\u0000) and files take a third more in base64: a
	// request at the limits fits; the limits themselves are checked on the decoded content.
	maxReq := 6*max(int64(cfg.Limits.MaxBody), int64(cfg.Limits.MaxReply)) + 2*int64(cfg.Limits.MaxFilesSize) + 1<<20
	var servers []*http.Server
	var cleanup []string
	errc := make(chan error, len(cfg.Node.Listen))
	for _, l := range cfg.Node.Listen {
		var ln net.Listener
		local := false
		switch {
		case strings.HasPrefix(l, "unix://"):
			path := strings.TrimPrefix(l, "unix://")
			ln, err = ListenUnix(path)
			if err != nil {
				return err
			}
			local = true
			pidFile := filepath.Join(filepath.Dir(path), "acpost.pid")
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
				return err
			}
			cleanup = append(cleanup, path, pidFile)
		default:
			ln, err = net.Listen("tcp", strings.TrimPrefix(l, "tcp://"))
			if err != nil {
				return err
			}
			if t := cfg.Node.TLS; t != nil {
				cert, err := tls.LoadX509KeyPair(t.Cert, t.Key)
				if err != nil {
					return fmt.Errorf("node.tls: %w", err)
				}
				ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
			}
		}
		srv := &http.Server{Handler: api.Handler(n, local, maxReq), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn)}
		servers = append(servers, srv)
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
		log.Info("listening", "on", l)
	}
	defer func() {
		for _, p := range cleanup {
			_ = os.Remove(p)
		}
	}()
	if err := n.Start(ctx); err != nil {
		return err
	}
	log.Info("node up", "name", cfg.Node.Name, "agents", len(cfg.Agents), "store", cfg.Store)
	if ready != nil {
		ready()
	}

	select {
	case <-ctx.Done():
		log.Info("stopping")
	case err := <-errc:
		log.Error("listener failed", "err", err)
	}
	sctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	for _, s := range servers {
		// Long polls end at once: their clients retry against the next node.
		_ = s.Shutdown(sctx)
		_ = s.Close()
	}
	n.Close()
	return nil
}

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
	_ = os.Remove(path)
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	return ln, err
}

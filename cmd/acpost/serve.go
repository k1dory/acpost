package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/k1dory/acpost/internal/api"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

func serveCmd(g *globals) *cobra.Command {
	var join string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a node (or a hub): the store, the agents and the API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(g.configPath())
			if err != nil {
				return usageErr(err)
			}
			if join != "" {
				tok := os.Getenv("ACPOST_JOIN_TOKEN")
				if tok == "" {
					return usageErr(errors.New("--join needs the hub's token in ACPOST_JOIN_TOKEN"))
				}
				cfg.Node.Join = &config.Join{URL: join, Token: tok}
			}
			level := slog.LevelInfo
			if verbose {
				level = slog.LevelDebug
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
			return serve(cfg, log)
		},
	}
	cmd.Flags().StringVar(&join, "join", "", "join a hub (`URL`); its token is read from ACPOST_JOIN_TOKEN")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "log agents' stderr and protocol details")
	return cmd
}

func serve(cfg *config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	st, err := store.Open(cfg.Store, node.StoreOptions(cfg))
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := node.New(cfg, st, log)
	if err != nil {
		return err
	}

	// JSON may spell one byte of text in six (\u0000): a request carrying a body or reply at its
	// limit fits; the limits themselves are checked on the decoded text.
	maxReq := 6*max(int64(cfg.Limits.MaxBody), int64(cfg.Limits.MaxReply)) + 1<<20
	var servers []*http.Server
	var cleanup []string
	errc := make(chan error, len(cfg.Node.Listen))
	for _, l := range cfg.Node.Listen {
		var ln net.Listener
		local := false
		switch {
		case strings.HasPrefix(l, "unix://"):
			path := strings.TrimPrefix(l, "unix://")
			ln, err = listenUnix(path)
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

// listenUnix listens on a Unix socket in a private directory, replacing a stale socket file.
func listenUnix(path string) (net.Listener, error) {
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

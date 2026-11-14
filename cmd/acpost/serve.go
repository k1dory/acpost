package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/server"
)

func serveCmd(g *globals) *cobra.Command {
	var join string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a node (or a hub): the store, the agents and the API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.loadConfig()
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
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer stop()
			// The agents the node starts find this command as `acpost` (node/clipath.go).
			if self, err := os.Executable(); err == nil {
				node.CLI = self
			}
			return server.Run(ctx, cfg, log, nil)
		},
	}
	cmd.Flags().StringVar(&join, "join", "", "join a hub (`URL`); its token is read from ACPOST_JOIN_TOKEN")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "log agents' stderr and protocol details")
	return cmd
}

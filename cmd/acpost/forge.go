package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/forge"
)

func forgeCmd(g *globals) *cobra.Command {
	var q client.ForgeQuery
	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Pull requests, CI runs and logs of a repository's forge (GitHub, GitLab, Forgejo/Gitea)",
	}
	cmd.PersistentFlags().StringVar(&q.Agent, "agent", "", "the repository an agent works in")
	cmd.PersistentFlags().StringVar(&q.Dir, "dir", "", "the repository in this directory (default: here)")
	query := func() client.ForgeQuery {
		if q.Agent == "" && q.Dir == "" {
			q.Dir, _ = os.Getwd()
		} else if q.Dir != "" {
			q.Dir, _ = filepath.Abs(q.Dir)
		}
		return q
	}

	var kind string
	login := &cobra.Command{
		Use:   "login HOST",
		Short: "Keep a token for a forge host on this machine's node, once the forge accepts it (macOS: in the keychain); read from the terminal or stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.ownerOnly(); err != nil {
				return err // before a token is asked for
			}
			var token string
			if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
				fmt.Fprintf(os.Stderr, "token for %s: ", args[0])
				b, err := term.ReadPassword(fd)
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return err
				}
				token = string(b)
			} else {
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				token = line
			}
			if strings.TrimSpace(token) == "" {
				return usageErr(errors.New("no token given"))
			}
			// The node keeps it, where it reads tokens (its keychain or file, its machine for
			// --node), and only once the forge says whose it is: a wrong token is not kept.
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			acc, err := c.ForgeLogin(cmd.Context(), args[0], kind, token)
			if err != nil {
				return err
			}
			if g.json {
				printJSON(acc)
				return nil
			}
			fmt.Fprintf(os.Stderr, "· kept for %s (%s %s), the token of %s\n", acc.Host, acc.Kind, acc.Version, acc.Login)
			return nil
		},
	}
	login.Flags().StringVar(&kind, "kind", "", "github, gitlab or forgejo (default: ask the host)")
	logout := &cobra.Command{
		Use:         "logout HOST",
		Annotations: quick,
		Short:       "Forget a forge host's token (on the node)",
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := g.ownerOnly(); err != nil {
				return err
			}
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			return c.ForgeLogout(cmd.Context(), args[0])
		},
	}

	status := &cobra.Command{
		Use:         "status",
		Annotations: quick,
		Short:       "The repository's forge: host, kind, version, branch, whether a token is kept",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			info, err := c.Forge(cmd.Context(), query())
			if err != nil {
				return err
			}
			if g.json {
				printJSON(info)
				return nil
			}
			fmt.Printf("%s %s (%s %s), branch %s, token %v\n", info.Repo.Host, info.Repo.Path, info.Server.Kind, info.Server.Version, info.Branch, info.Authed)
			return nil
		},
	}

	var state string
	prs := &cobra.Command{
		Use:   "prs",
		Short: "Pull (merge) requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			l, err := c.PullList(cmd.Context(), query(), state)
			if err != nil {
				return err
			}
			defer noteCut(l.Truncated, len(l.Items), "pull requests")
			if g.json {
				printJSON(nonNil(l.Items))
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, p := range l.Items {
				fmt.Fprintf(w, "#%d\t%s\t%s → %s\t%s\t%s\n", p.Number, p.State, p.Head, p.Base, p.Author, p.Title)
			}
			return w.Flush()
		},
	}
	prs.Flags().StringVar(&state, "state", "open", "open, closed, merged (GitLab) or all")

	var ref string
	checks := &cobra.Command{
		Use:   "checks",
		Short: "CI runs of a branch or commit (default: the repository's branch), with their jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			l, err := c.CheckList(cmd.Context(), query(), ref)
			if err != nil {
				return err
			}
			defer noteCut(l.Truncated, len(l.Items), "runs")
			if g.json {
				printJSON(nonNil(l.Items))
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, ch := range l.Items {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ch.ID, ch.Status, ch.Name, took(ch.Started, ch.Finished))
				for _, j := range ch.Jobs {
					fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", j.ID, j.Status, j.Name, took(j.Started, j.Finished))
				}
				if ch.JobsError != "" {
					fmt.Fprintf(w, "  !\t%s\n", ch.JobsError)
				}
			}
			return w.Flush()
		},
	}
	checks.Flags().StringVar(&ref, "ref", "", "a branch or commit")

	var target string
	logCmd := &cobra.Command{
		Use:   "log JOB",
		Short: "A CI job's log (its secrets masked)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			text, err := c.CILog(cmd.Context(), query(), args[0], target)
			if err != nil {
				return err
			}
			if g.json {
				printJSON(map[string]string{"job": args[0], "log": text})
				return nil
			}
			fmt.Print(text)
			return nil
		},
	}
	logCmd.Flags().StringVar(&target, "target", "", "the check's URL (Forgejo/Gitea)")

	var pr forge.PullRequest
	open := &cobra.Command{
		Use:   "open",
		Short: "Open a pull request from the repository's branch (or --head)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			p, err := c.OpenPull(cmd.Context(), query(), pr)
			if err != nil {
				return err
			}
			if g.json {
				printJSON(p)
				return nil
			}
			if p.Existing {
				fmt.Fprintf(os.Stderr, "· #%d was already open from %s\n", p.Number, p.Head)
			}
			fmt.Println(p.URL)
			return nil
		},
	}
	open.Flags().StringVar(&pr.Title, "title", "", "its title")
	open.Flags().StringVar(&pr.Base, "base", "", "the branch it goes into (default: the repository's default branch)")
	open.Flags().StringVar(&pr.Head, "head", "", "the branch it comes from (default: the repository's)")
	open.Flags().StringVar(&pr.Body, "body", "", "its description")
	open.Flags().BoolVar(&pr.Draft, "draft", false, "a draft")

	var mg forge.Merge
	merge := &cobra.Command{
		Use:   "merge NUMBER",
		Short: "Merge a pull request (run by an agent: once a person approves it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil || num <= 0 {
				return fmt.Errorf("%q: a pull request's number", args[0])
			}
			mg.Number = num
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			if err := c.MergePull(cmd.Context(), query(), mg); err != nil {
				return err
			}
			if g.json {
				printJSON(map[string]any{"merged": true, "number": num})
				return nil
			}
			fmt.Printf("merged #%d\n", num)
			return nil
		},
	}
	merge.Flags().StringVar(&mg.Method, "method", "merge", "merge, squash or rebase")
	merge.Flags().StringVar(&mg.SHA, "sha", "", "merge only if the head is this commit")

	cmd.AddCommand(login, logout, status, prs, checks, logCmd, open, merge)
	return cmd
}

func took(start, end time.Time) string {
	if start.IsZero() {
		return ""
	}
	if end.IsZero() {
		return "running " + time.Since(start).Round(time.Second).String()
	}
	return end.Sub(start).Round(time.Second).String()
}

// noteCut says on stderr that a list holds only the first of what the forge has.
func noteCut(cut bool, n int, what string) {
	if cut {
		fmt.Fprintf(os.Stderr, "more: the forge has more %s than these %d\n", what, n)
	}
}

// nonNil is a list as JSON shows it: [] for none, not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ownerOnly refuses forge accounts over anything but the node's unix socket: the node keeps them
// for its own user only, and over TCP every token is a client, an agent or a node (refused there
// in any case, after a token was typed for nothing).
func (g *globals) ownerOnly() error {
	url, _, err := g.nodeURL()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(url, "unix://") {
		return &exitErr{code: exitRefused, name: "refused", err: fmt.Errorf("forge accounts are kept by the node for its own user, over its unix socket, not over %s", url),
			hint: "run `acpost forge login` on the node's machine (or in its app's Settings)"}
	}
	return nil
}

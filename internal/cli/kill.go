package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/paths"
	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/terminal"
)

// sessionPrefix starts every terminal session Wrekin creates.
const sessionPrefix = "wrekin-"

func newKillCommand(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "kill [<session>|<job>]...",
		Short: "End teleported sessions on this machine",
		Long: "Kills the tmux session of each job or session named (see `wrekin sessions`), or of every Wrekin session with --all, " +
			"those an earlier daemon left behind included. Their jobs end as session_gone, so teleporting the same cloud session " +
			"again needs relaunch. Worktrees stay; `wrekin cleanup` removes them.",
		ValidArgsFunction: completeSessionsAndJobs(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) > 0) {
				return errors.New("name the sessions or jobs to kill, or pass --all")
			}
			client := control.NewClient(paths.SocketFile())
			term, termErr := terminal.Lookup("tmux")
			targets := args
			if all {
				var err error
				if targets, err = liveSessions(cmd, client, term, termErr); err != nil {
					return err
				}
				if len(targets) == 0 {
					fmt.Fprintln(a.stdout, "No Wrekin session is running.")
					return nil
				}
			}
			failed := false
			for _, target := range targets {
				var res control.SessionsKillResult
				err := client.Call(cmd.Context(), control.MethodSessionsKill, control.SessionsKillParams{Target: target}, &res)
				if errors.Is(err, control.ErrNotRunning) {
					// No daemon to update the jobs: the session itself still goes.
					err = killDirectly(term, termErr, target)
					res.Session = target
				}
				if err != nil {
					fmt.Fprintf(a.stdout, "%s: %v\n", target, err)
					failed = true
					continue
				}
				if res.Job != nil {
					fmt.Fprintf(a.stdout, "Killed %s (job %s).\n", res.Session, res.Job.ID)
				} else {
					fmt.Fprintf(a.stdout, "Killed %s.\n", res.Session)
				}
			}
			if failed {
				return errSilent
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "kill every Wrekin session on this machine")
	return cmd
}

// liveSessions is every session --all ends: those of live jobs, and every
// terminal session named wrekin-… (an earlier daemon's included).
func liveSessions(cmd *cobra.Command, client *control.Client, term terminal.Terminal, termErr error) ([]string, error) {
	seen := map[string]bool{}
	var res control.SessionsResult
	err := client.Call(cmd.Context(), control.MethodSessions, nil, &res)
	if err != nil && !errors.Is(err, control.ErrNotRunning) {
		return nil, err
	}
	for _, j := range res.Jobs {
		if j.Session != nil && (j.State == protocol.StateReady || j.State == protocol.StateLaunching) {
			seen[j.Session.Name] = true
		}
	}
	if termErr == nil {
		if names, err := term.List(sessionPrefix); err == nil {
			for _, n := range names {
				seen[n] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func killDirectly(term terminal.Terminal, termErr error, session string) error {
	if termErr != nil {
		return termErr
	}
	if !sessionNameRe.MatchString(session) {
		return fmt.Errorf("invalid session name %q", session)
	}
	exists, err := term.Exists(session)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no session of that name (the daemon is not running, so job ids are not known here)")
	}
	return term.Kill(session)
}

package cli

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/paths"
	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/repos"
	"github.com/plmercereau/wrekin/internal/terminal"
)

// Shell completion: cobra's own `wrekin completion bash|zsh|fish|powershell`
// prints the scripts; the functions below fill the arguments in. They answer
// fast and quietly: no daemon, no tmux or no learned mapping means fewer
// candidates, never an error in the user's shell.

// completionTimeout bounds what a completion asks the daemon.
const completionTimeout = 500 * time.Millisecond

// liveSessionNames are the Wrekin terminal sessions on this machine.
func (a *app) liveSessionNames() []string {
	if a.completeSessions != nil {
		return a.completeSessions()
	}
	term, err := terminal.Lookup("tmux")
	if err != nil {
		return nil
	}
	names, err := term.List(sessionPrefix)
	if err != nil {
		return nil
	}
	return names
}

// liveJobIDs are the jobs whose session runs, from the daemon when it runs.
func (a *app) liveJobIDs(ctx context.Context) []string {
	if a.completeJobs != nil {
		return a.completeJobs()
	}
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()
	var res control.SessionsResult
	if err := control.NewClient(paths.SocketFile()).Call(ctx, control.MethodSessions, nil, &res); err != nil {
		return nil
	}
	var ids []string
	for _, j := range res.Jobs {
		if j.Session != nil && (j.State == protocol.StateReady || j.State == protocol.StateLaunching) {
			ids = append(ids, j.ID)
		}
	}
	return ids
}

// learnedRepositories are the repositories of the learned mappings.
func (a *app) learnedRepositories() []string {
	if a.completeRepos != nil {
		return a.completeRepos()
	}
	entries, err := repos.New(paths.RepositoriesFile()).List()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Repository)
	}
	return out
}

// candidates keeps the names that start with what was typed and are not
// among the arguments already given.
func candidates(names, given []string, typed string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, typed) && !slices.Contains(given, n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// completeOneSession: `wrekin attach <session>`.
func completeOneSession(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return candidates(a.liveSessionNames(), nil, typed), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeSessionsAndJobs: `wrekin kill <session|job>...`, any number of them.
func completeSessionsAndJobs(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective) {
		if all, _ := cmd.Flags().GetBool("all"); all {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := append(a.liveSessionNames(), a.liveJobIDs(cmd.Context())...)
		return candidates(names, args, typed), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeLearnedRepository: `wrekin repos forget|pin <repository>`.
func completeLearnedRepository(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, typed string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return candidates(a.learnedRepositories(), nil, typed), cobra.ShellCompDirectiveNoFileComp
	}
}

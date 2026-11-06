package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/paths"
)

func newCleanupCommand(a *app) *cobra.Command {
	var yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove the tmux sessions, worktrees and clones Wrekin created",
		Long:  "Removes only what Wrekin created for finished jobs: their tmux sessions, the worktrees it added and the repositories it cloned. Repositories that existed before Wrekin are never touched.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := control.NewClient(paths.SocketFile())
			var preview control.CleanupReport
			err := client.Call(cmd.Context(), control.MethodCleanup, control.CleanupParams{DryRun: true}, &preview)
			if errors.Is(err, control.ErrNotRunning) {
				explainNotRunning(a)
				return errSilent
			}
			if err != nil {
				return err
			}
			printReport(a, preview)
			if dryRun || len(preview.Jobs) == 0 {
				return nil
			}
			if !yes {
				fmt.Fprint(a.stdout, "Proceed? [y/N] ")
				line, _ := bufio.NewReader(a.stdin).ReadString('\n')
				if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
					fmt.Fprintln(a.stdout, "Nothing removed.")
					return nil
				}
			}
			var report control.CleanupReport
			if err := client.Call(cmd.Context(), control.MethodCleanup, control.CleanupParams{DryRun: false}, &report); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Removed %d session(s), %d worktree(s), %d clone(s); forgot %d job(s).\n", len(report.Sessions), len(report.Worktrees), len(report.Clones), len(report.Jobs))
			for _, e := range report.Errors {
				fmt.Fprintln(a.stderr, "warning:", e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only show what would be removed")
	return cmd
}

func printReport(a *app, r control.CleanupReport) {
	if len(r.Jobs) == 0 {
		fmt.Fprintln(a.stdout, "Nothing to clean up.")
		return
	}
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(a.stdout, "%s:\n", title)
		for _, it := range items {
			fmt.Fprintf(a.stdout, "  %s\n", it)
		}
	}
	section("tmux sessions to kill", r.Sessions)
	section("worktrees to remove", r.Worktrees)
	section("clones to delete", r.Clones)
	section("jobs to forget", r.Jobs)
	section("jobs still running (skipped)", r.Skipped)
}

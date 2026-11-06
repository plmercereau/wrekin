package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/paths"
)

func newSessionsCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List the teleport jobs and their tmux sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			var res control.SessionsResult
			err := control.NewClient(paths.SocketFile()).Call(cmd.Context(), control.MethodSessions, nil, &res)
			if errors.Is(err, control.ErrNotRunning) {
				explainNotRunning(a)
				return errSilent
			}
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(a.stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res.Jobs)
			}
			if len(res.Jobs) == 0 {
				fmt.Fprintln(a.stdout, "No teleport jobs yet.")
				return nil
			}
			w := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "JOB\tSTATE\tREPOSITORY\tSESSION\tUPDATED")
			for _, j := range res.Jobs {
				repo, session := "-", "-"
				if j.Repository != nil {
					repo = j.Repository.Name
				}
				if j.Session != nil {
					session = j.Session.Name
				}
				state := string(j.State)
				if j.Error != nil {
					state += " (" + j.Error.Code + ")"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", j.ID, state, repo, session, j.UpdatedAt.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the jobs as JSON")
	return cmd
}

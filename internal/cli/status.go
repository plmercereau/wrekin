package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/paths"
)

func newStatusCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the agent's connection and job summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			var st control.StatusInfo
			err := control.NewClient(paths.SocketFile()).Call(cmd.Context(), control.MethodStatus, nil, &st)
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
				return enc.Encode(st)
			}
			fmt.Fprintf(a.stdout, "wrekin %s, pid %d, up since %s\n", st.Version, st.PID, st.StartedAt.Local().Format(time.RFC3339))
			fmt.Fprintf(a.stdout, "node %s (%s), account %s\n", st.Name, st.NodeID, st.AccountID)
			switch {
			case st.Relay.ConnectedSince.IsZero():
				fmt.Fprintf(a.stdout, "relay: %s", st.Relay.Status)
				if st.Relay.LastError != "" {
					fmt.Fprintf(a.stdout, " (%s)", st.Relay.LastError)
				}
				fmt.Fprintln(a.stdout)
			default:
				fmt.Fprintf(a.stdout, "relay: %s since %s\n", st.Relay.Status, st.Relay.ConnectedSince.Local().Format(time.RFC3339))
			}
			if len(st.Jobs) == 0 {
				fmt.Fprintln(a.stdout, "jobs: none")
				return nil
			}
			states := make([]string, 0, len(st.Jobs))
			for s := range st.Jobs {
				states = append(states, s)
			}
			sort.Strings(states)
			fmt.Fprint(a.stdout, "jobs:")
			for _, s := range states {
				fmt.Fprintf(a.stdout, " %s=%d", s, st.Jobs[s])
			}
			fmt.Fprintln(a.stdout)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the status as JSON")
	return cmd
}

// explainNotRunning tells the user what to do when no daemon answers.
func explainNotRunning(a *app) {
	fmt.Fprintln(a.stdout, "The wrekin agent is not running.")
	id, err := identity.Load(paths.IdentityFile())
	switch {
	case err == nil && id.Paired():
		fmt.Fprintf(a.stdout, "This machine is paired as node %s (%s). Start the agent with `wrekin start`.\n", id.NodeID, id.Name)
	default:
		fmt.Fprintln(a.stdout, "This machine is not paired yet: run `wrekin auth`, then `wrekin start`.")
	}
}

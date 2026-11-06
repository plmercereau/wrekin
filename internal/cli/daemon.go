package cli

import "github.com/spf13/cobra"

func newDaemonCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:    "daemon",
		Short:  "Run the node agent in the foreground (used by the service manager)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(cmd.Context(), a)
		},
	}
}

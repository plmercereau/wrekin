package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/paths"
	"github.com/plmercereau/wrekin/internal/service"
)

func newStopCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the node agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := service.Detect()
			if st, _ := mgr.Status(); st.Installed {
				if err := mgr.Stop(); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "Stopped the wrekin agent through %s.\n", mgr.Name())
				return nil
			}
			err := control.NewClient(paths.SocketFile()).Call(cmd.Context(), control.MethodShutdown, nil, nil)
			if errors.Is(err, control.ErrNotRunning) {
				fmt.Fprintln(a.stdout, "The wrekin agent is not running.")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(a.stdout, "Asked the wrekin agent to stop.")
			return nil
		},
	}
}

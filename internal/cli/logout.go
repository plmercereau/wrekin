package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/config"
	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/pairing"
	"github.com/plmercereau/wrekin/internal/paths"
)

func newLogoutCommand(a *app) *cobra.Command {
	var keepRemote bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Unregister this node and delete its identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := identity.Load(paths.IdentityFile())
			if errors.Is(err, identity.ErrNotPaired) {
				fmt.Fprintln(a.stdout, "This machine is not paired.")
				return nil
			}
			if err != nil {
				return err
			}
			if id.Paired() && !keepRemote {
				settings, err := config.Load(a.settingsPath)
				if err != nil {
					return err
				}
				p := &pairing.Pairer{BaseURL: settings.HTTPBaseURL(), AgentVersion: a.version, Out: a.stdout}
				if err := p.Unregister(cmd.Context(), id); err != nil {
					return fmt.Errorf("unregister node %s: %w (use --keep-remote to delete the local identity anyway)", id.NodeID, err)
				}
				fmt.Fprintf(a.stdout, "Unregistered node %s.\n", id.NodeID)
			}
			if err := os.Remove(paths.IdentityFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			fmt.Fprintln(a.stdout, "Local identity deleted. Stop the agent with `wrekin stop` if it is still running.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepRemote, "keep-remote", false, "delete the local identity without unregistering from the account")
	return cmd
}

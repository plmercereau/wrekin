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

func newAuthCommand(a *app) *cobra.Command {
	var name, relayURL string
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Pair this machine with your Wrekin account",
		Long:  "Generates the node key pair if needed, shows a pairing code to approve on another device, and registers the public key. The private key never leaves this machine.",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.Load(a.settingsPath)
			if err != nil {
				return err
			}
			if name != "" {
				settings.Name = name
			}
			if relayURL != "" {
				settings.Relay.URL = relayURL
			}
			if err := settings.Validate(); err != nil {
				return err
			}

			id, err := identity.Load(paths.IdentityFile())
			switch {
			case errors.Is(err, identity.ErrNotPaired):
				id, err = identity.Generate(settings.Name)
				if err != nil {
					return err
				}
			case err != nil:
				return err
			case id.Paired():
				fmt.Fprintf(a.stdout, "This machine is already paired as node %s (account %s).\nRun `wrekin logout` first to pair it again.\n", id.NodeID, id.AccountID)
				return nil
			}

			p := &pairing.Pairer{BaseURL: settings.HTTPBaseURL(), AgentVersion: a.version, Out: a.stdout}
			nodeID, accountID, err := p.Pair(cmd.Context(), settings.Name, id)
			if err != nil {
				return err
			}
			id.NodeID, id.AccountID, id.Name, id.RelayURL = nodeID, accountID, settings.Name, settings.Relay.URL
			if err := id.Save(paths.IdentityFile()); err != nil {
				return fmt.Errorf("save identity: %w", err)
			}
			if _, err := os.Stat(a.settingsPath); errors.Is(err, os.ErrNotExist) {
				toSave := config.Default()
				toSave.Name = settings.Name
				toSave.Relay.URL = settings.Relay.URL
				if err := config.Save(a.settingsPath, toSave); err != nil {
					return fmt.Errorf("write settings: %w", err)
				}
				fmt.Fprintf(a.stdout, "Wrote %s; edit workspace.allowedDirectories to choose where teleports may land.\n", a.settingsPath)
			}
			fmt.Fprintf(a.stdout, "Paired as node %s (%s). Start the agent with `wrekin start`.\n", nodeID, settings.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "node name shown in Claude (default: hostname)")
	cmd.Flags().StringVar(&relayURL, "relay", "", "relay URL (default "+config.DefaultRelayURL+")")
	return cmd
}

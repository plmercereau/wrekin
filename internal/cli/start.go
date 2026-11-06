package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/daemon"
	"github.com/plmercereau/wrekin/internal/service"
)

func newStartCommand(a *app) *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the node agent (as a user service, or in the foreground)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if foreground {
				return runDaemon(cmd.Context(), a)
			}
			mgr := service.Detect()
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if err := mgr.Install(exe); err != nil {
				return err
			}
			if err := mgr.Start(); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Started the wrekin agent through %s. Check it with `wrekin status`.\n", mgr.Name())
			return nil
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run the daemon in this terminal instead of installing a service")
	return cmd
}

// runDaemon runs the daemon until SIGINT or SIGTERM.
func runDaemon(parent context.Context, a *app) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return daemon.Run(ctx, daemon.Options{SettingsPath: a.settingsPath, Version: a.version, Stderr: a.stderr})
}

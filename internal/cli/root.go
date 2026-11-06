// Package cli implements the wrekin command-line interface with cobra; one
// file per command.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Execute runs the root command and exits the process on error.
func Execute(version string) {
	if err := newRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "wrekin:", err)
		os.Exit(1)
	}
}

func newRootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "wrekin",
		Short:         "Teleport Claude Code Cloud sessions onto this machine",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return root
}

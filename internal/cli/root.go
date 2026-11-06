// Package cli implements the wrekin command-line interface with cobra; one
// file per command.
package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/paths"
)

// app carries what every command needs.
type app struct {
	version      string
	settingsPath string
	stdout       io.Writer
	stderr       io.Writer
	stdin        io.Reader
	// httpClient talks to the cloud; nil means the pairing default (tests
	// inject an in-process transport).
	httpClient *http.Client
	// claude is the Claude Code adapter `wrekin auth` signs in; nil means the
	// real one (tests inject a fake).
	claude claudeSignIn
	// interactive overrides the terminal check on stdin (tests).
	interactive func() bool
}

// errSilent marks errors already explained on stdout; Execute only sets the
// exit code for them.
var errSilent = errors.New("silent")

// Execute runs the root command and exits the process on error.
func Execute(version string) {
	root := newRootCommand(&app{version: version, stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin})
	if err := root.Execute(); err != nil {
		if !errors.Is(err, errSilent) {
			fmt.Fprintln(os.Stderr, "wrekin:", err)
		}
		os.Exit(1)
	}
}

func newRootCommand(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "wrekin",
		Short:         "Teleport Claude Code Cloud sessions onto this machine",
		Long:          "Wrekin keeps this machine connected to the Wrekin relay so that a Claude Code Cloud session can be teleported here, into a tmux session, from Claude.",
		Version:       a.version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetIn(a.stdin)
	root.PersistentFlags().StringVar(&a.settingsPath, "settings", paths.SettingsFile(), "path of settings.json")
	root.AddCommand(
		newAuthCommand(a),
		newStartCommand(a),
		newStopCommand(a),
		newStatusCommand(a),
		newSessionsCommand(a),
		newAttachCommand(a),
		newKillCommand(a),
		newLogsCommand(a),
		newCleanupCommand(a),
		newReposCommand(a),
		newLogoutCommand(a),
		newDaemonCommand(a),
	)
	return root
}

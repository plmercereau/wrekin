package cli

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/terminal"
)

var sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func newAttachCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "attach <session>",
		Short:             "Attach to a teleported session (replaces this process with tmux)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeOneSession(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			session := args[0]
			if !sessionNameRe.MatchString(session) {
				return fmt.Errorf("invalid session name %q", session)
			}
			term, err := terminal.Lookup("tmux")
			if err != nil {
				return err
			}
			if err := term.Available(); err != nil {
				return err
			}
			exists, err := term.Exists(session)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("no tmux session called %q; see `wrekin sessions`", session)
			}
			argv := term.AttachCommand(session)
			bin, err := exec.LookPath(argv[0])
			if err != nil {
				return err
			}
			return syscall.Exec(bin, argv, os.Environ())
		},
	}
}

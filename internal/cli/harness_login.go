package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/plmercereau/wrekin/internal/harness/claude"
)

// claudeSignIn is what `wrekin auth` needs from the Claude Code adapter.
type claudeSignIn interface {
	SignedIn(ctx context.Context) (bool, error)
	Login(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error
	CompleteOnboarding() (bool, error)
}

func (a *app) claudeHarness() claudeSignIn {
	if a.claude != nil {
		return a.claude
	}
	return claude.New("", "claude")
}

// interactiveStdin reports whether stdin is a terminal, where a login can
// prompt and wait.
func (a *app) interactiveStdin() bool {
	if a.interactive != nil {
		return a.interactive()
	}
	f, ok := a.stdin.(*os.File)
	return ok && isTerminal(f.Fd())
}

// ensureClaudeSignedIn makes sure Claude Code can run on this machine: a
// teleport launches `claude --teleport`, which needs the user's credentials,
// and would otherwise fail in the terminal after everything else was done.
// When Claude Code is signed out and the session is interactive, it runs
// `claude auth login`; otherwise it says what to run. It never fails
// `wrekin auth`: the pairing stands either way.
func ensureClaudeSignedIn(ctx context.Context, a *app, allowLogin bool) {
	h := a.claudeHarness()
	signedIn, err := h.SignedIn(ctx)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		fmt.Fprintln(a.stdout, "Claude Code (`claude`) is not on this PATH: install it, then run `claude auth login` before teleporting here.")
		return
	case err != nil:
		// An older Claude Code without `auth status`: nothing to tell.
		return
	case signedIn:
		fmt.Fprintln(a.stdout, "Claude Code is signed in on this machine.")
		completeOnboarding(a, h)
		return
	}
	if !allowLogin || !a.interactiveStdin() {
		fmt.Fprintln(a.stdout, "Claude Code is not signed in on this machine: run `claude auth login` here before teleporting, or teleports will fail.")
		return
	}
	fmt.Fprintln(a.stdout, "Claude Code is not signed in on this machine; running `claude auth login`.")
	if err := h.Login(ctx, a.stdin, a.stdout, a.stderr); err != nil {
		fmt.Fprintf(a.stdout, "`claude auth login` did not complete (%v); run it again before teleporting here.\n", err)
		return
	}
	if ok, err := h.SignedIn(ctx); err == nil && !ok {
		fmt.Fprintln(a.stdout, "Claude Code still reports no sign-in; run `claude auth login` again before teleporting here.")
		return
	}
	fmt.Fprintln(a.stdout, "Claude Code is signed in on this machine.")
	completeOnboarding(a, h)
}

// completeOnboarding marks Claude Code's first-run screens as done: a
// teleport starts it in a detached terminal, where its theme picker would
// wait until the user attached.
func completeOnboarding(a *app, h claudeSignIn) {
	changed, err := h.CompleteOnboarding()
	switch {
	case err != nil:
		fmt.Fprintf(a.stdout, "Could not mark Claude Code's first-run setup as done (%v); run `claude` once here and finish it before teleporting.\n", err)
	case changed:
		fmt.Fprintln(a.stdout, "Marked Claude Code's first-run setup as done, so a teleport opens the session rather than the theme picker (`/theme` changes it).")
	}
}

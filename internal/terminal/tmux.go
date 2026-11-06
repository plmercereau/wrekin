package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Tmux drives the tmux binary.
type Tmux struct {
	// Binary is the tmux executable; "tmux" by default.
	Binary string
	// SocketPath selects the tmux server by its socket path (-S); it wins
	// over SocketName. Lookup sets it to paths.TmuxSocket().
	SocketPath string
	// SocketName selects a separate tmux server (-L) in tmux's socket
	// directory, which depends on TMUX_TMPDIR (tests); empty with no
	// SocketPath means tmux's default server.
	SocketName string
}

// NewTmux returns a Tmux using the default server.
func NewTmux() *Tmux { return &Tmux{Binary: "tmux"} }

// Name implements Terminal.
func (t *Tmux) Name() string { return "tmux" }

// Available implements Terminal.
func (t *Tmux) Available() error {
	if _, err := exec.LookPath(t.Binary); err != nil {
		return fmt.Errorf("%w: tmux was not found in PATH", ErrUnavailable)
	}
	return nil
}

func (t *Tmux) args(rest ...string) []string {
	switch {
	case t.SocketPath != "":
		return append([]string{"-S", t.SocketPath}, rest...)
	case t.SocketName != "":
		return append([]string{"-L", t.SocketName}, rest...)
	default:
		return rest
	}
}

// run executes a tmux command and returns its stdout. tmux sometimes reports
// a failure on stderr while exiting 0 (for instance when it cannot create its
// server socket), so stderr is returned too for callers that must check it.
func (t *Tmux) run(ctx context.Context, rest ...string) (stdout string, stderr string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.Binary, t.args(rest...)...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	stderr = strings.TrimSpace(errBuf.String())
	if runErr := cmd.Run(); runErr != nil {
		stderr = strings.TrimSpace(errBuf.String())
		msg := stderr
		if msg == "" {
			msg = runErr.Error()
		}
		return "", stderr, fmt.Errorf("tmux %s: %s", rest[0], msg)
	}
	return strings.TrimRight(out.String(), "\n"), strings.TrimSpace(errBuf.String()), nil
}

// target names a session exactly (the "=" prefix disables prefix matching),
// for the commands that take a target-session: has-session, kill-session,
// attach-session.
func target(session string) string { return "=" + session }

// paneTarget names the session's current pane exactly, for the commands that
// take a target-pane or target-window (capture-pane, display-message,
// set-option): without the trailing colon tmux reads "=name" as a window or
// pane name and answers "can't find pane".
func paneTarget(session string) string { return "=" + session + ":" }

// Launch implements Terminal. argv is quoted into a single shell command,
// which every tmux version accepts.
func (t *Tmux) Launch(ctx context.Context, session, workdir string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("tmux: empty command")
	}
	if t.SocketPath != "" {
		// The first launch starts the server, which creates the socket but
		// not its directory.
		if err := os.MkdirAll(filepath.Dir(t.SocketPath), 0o700); err != nil {
			return fmt.Errorf("tmux socket directory: %w", err)
		}
	}
	_, stderr, err := t.run(ctx, launchArgs(session, workdir, argv)...)
	if err != nil {
		return err
	}
	exists, err := t.Exists(session)
	if err != nil {
		return err
	}
	if !exists {
		if stderr == "" {
			stderr = "the session is not there after new-session"
		}
		return fmt.Errorf("tmux new-session: %s", stderr)
	}
	// Best effort, in a call of its own: chained after new-session in the
	// same invocation, tmux answered "no such session" for the session it had
	// just created.
	_, _, _ = t.run(ctx, keepAliveArgs(session)...)
	return nil
}

// Exists implements Terminal.
func (t *Tmux) Exists(session string) (bool, error) {
	cmd := exec.Command(t.Binary, t.args("has-session", "-t", target(session))...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil // "can't find session" or "no server running"
	}
	return false, fmt.Errorf("tmux has-session: %w", err)
}

// shells are the pane commands that mean the launched program is gone.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true}

// Running implements Terminal.
func (t *Tmux) Running(session string) (bool, error) {
	exists, err := t.Exists(session)
	if err != nil || !exists {
		return false, err
	}
	out, _, err := t.run(nil, "display-message", "-p", "-t", paneTarget(session), "#{pane_dead}\t#{pane_current_command}")
	if err != nil {
		return false, err
	}
	dead, command, _ := strings.Cut(out, "\t")
	if dead == "1" {
		return false, nil
	}
	command = strings.TrimPrefix(strings.TrimSpace(command), "-")
	if command == "" || shells[command] {
		return false, nil
	}
	return true, nil
}

// Capture implements Terminal.
func (t *Tmux) Capture(session string, lines int) (string, error) {
	if lines <= 0 {
		lines = 40
	}
	out, _, err := t.run(nil, "capture-pane", "-p", "-t", paneTarget(session), "-S", "-"+strconv.Itoa(lines))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, " \n\t"), nil
}

// Kill implements Terminal.
func (t *Tmux) Kill(session string) error {
	exists, err := t.Exists(session)
	if err != nil || !exists {
		return err
	}
	_, _, err = t.run(nil, "kill-session", "-t", target(session))
	return err
}

// List implements Terminal.
func (t *Tmux) List(prefix string) ([]string, error) {
	cmd := exec.Command(t.Binary, t.args("list-sessions", "-F", "#{session_name}")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil // no server running: no sessions
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line != "" && strings.HasPrefix(line, prefix) {
			names = append(names, line)
		}
	}
	return names, nil
}

// AttachCommand implements Terminal.
func (t *Tmux) AttachCommand(session string) []string {
	return append([]string{t.Binary}, t.args("attach-session", "-t", target(session))...)
}

// killServer stops the whole tmux server (tests only, on a private socket).
func (t *Tmux) killServer() error {
	_, _, err := t.run(nil, "kill-server")
	return err
}

// launchArgs builds the new-session command: detached, in workdir, running
// the shell command for argv.
func launchArgs(session, workdir string, argv []string) []string {
	return []string{"new-session", "-d", "-s", session, "-c", workdir, shellJoin(argv)}
}

// keepAliveArgs tells the session to survive without an attached client even
// when the user's configuration sets destroy-unattached.
func keepAliveArgs(session string) []string {
	return []string{"set-option", "-t", paneTarget(session), "destroy-unattached", "off"}
}

// shellJoin quotes argv for a POSIX shell.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

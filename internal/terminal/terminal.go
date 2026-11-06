// Package terminal abstracts the persistent terminal that hosts a harness
// session. tmux is the only implementation.
package terminal

import (
	"context"
	"errors"
	"fmt"

	"github.com/plmercereau/wrekin/internal/paths"
)

// ErrUnavailable means the requested terminal is not installed or not
// supported.
var ErrUnavailable = errors.New("terminal unavailable")

// Terminal is a detached, re-attachable terminal multiplexer.
type Terminal interface {
	// Name is the terminal id used in Job.session.terminal ("tmux").
	Name() string
	// Available returns nil when the terminal can be used.
	Available() error
	// Launch starts a new detached session running argv in workdir.
	Launch(ctx context.Context, session, workdir string, argv []string) error
	// Exists reports whether the session is alive.
	Exists(session string) (bool, error)
	// Running reports whether the session's pane runs something other than a
	// shell, that is whether the launched program is still alive.
	Running(session string) (bool, error)
	// Capture returns the last lines of the session's pane.
	Capture(session string, lines int) (string, error)
	// SendLine types text into the session's pane, literally, then Enter.
	SendLine(session, text string) error
	// KeepOnExit makes the session's pane stay, with its output, after its
	// program exits (Launch turns it on), or go with it again.
	KeepOnExit(session string, keep bool) error
	// Kill terminates the session.
	Kill(session string) error
	// List returns the sessions whose name starts with prefix.
	List(prefix string) ([]string, error)
	// AttachCommand is the argv a user runs to attach to the session.
	AttachCommand(session string) []string
}

// Lookup returns the terminal implementation called name.
func Lookup(name string) (Terminal, error) {
	switch name {
	case "tmux":
		// The daemon and the CLI both come through here: one explicit socket.
		return &Tmux{Binary: "tmux", SocketPath: paths.TmuxSocket()}, nil
	default:
		return nil, fmt.Errorf("%w: %q is not supported (only tmux)", ErrUnavailable, name)
	}
}

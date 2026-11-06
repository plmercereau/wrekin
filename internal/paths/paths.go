// Package paths resolves the directories Wrekin uses for its configuration
// and its state. They follow the XDG base directory layout on both Linux and
// macOS: XDG_CONFIG_HOME and XDG_STATE_HOME override the two prefixes.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

const app = "wrekin"

// ConfigDir is where settings.json and identity.json live.
func ConfigDir() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, app)
	}
	return filepath.Join(home(), ".config", app)
}

// StateDir is where the job store, the log and the control socket live.
func StateDir() string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, app)
	}
	return filepath.Join(home(), ".local", "state", app)
}

// SettingsFile is the user configuration file.
func SettingsFile() string { return filepath.Join(ConfigDir(), "settings.json") }

// IdentityFile holds the node key pair and its pairing data (mode 0600).
func IdentityFile() string { return filepath.Join(ConfigDir(), "identity.json") }

// JobsFile is the persisted job store.
func JobsFile() string { return filepath.Join(StateDir(), "jobs.json") }

// LogFile is the daemon log.
func LogFile() string { return filepath.Join(StateDir(), "wrekin.log") }

// SocketFile is the unix socket the CLI uses to talk to the daemon.
func SocketFile() string { return filepath.Join(StateDir(), "wrekin.sock") }

// TmuxSocket is the socket of the tmux server the teleported sessions run
// in. The daemon and the CLI name it explicitly: tmux's own default depends
// on TMUX_TMPDIR, which a login shell often sets (NixOS's programs.tmux sets
// it to XDG_RUNTIME_DIR) and a systemd or launchd service does not, so the
// two would talk to different servers and `wrekin attach` would not find
// the session the daemon started.
func TmuxSocket() string { return filepath.Join(StateDir(), "tmux.sock") }

// RepositoriesFile holds the repository mappings the daemon learned.
func RepositoriesFile() string { return filepath.Join(StateDir(), "repositories.json") }

// ExpandHome replaces a leading "~" or "~/" with the user's home directory.
// Other forms ("~user") are left alone.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

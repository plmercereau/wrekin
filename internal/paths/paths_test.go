package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsHonourXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/cfg")
	t.Setenv("XDG_STATE_HOME", "/y/state")

	cases := map[string]string{
		ConfigDir():    "/x/cfg/wrekin",
		StateDir():     "/y/state/wrekin",
		SettingsFile(): "/x/cfg/wrekin/settings.json",
		IdentityFile(): "/x/cfg/wrekin/identity.json",
		JobsFile():     "/y/state/wrekin/jobs.json",
		LogFile():      "/y/state/wrekin/wrekin.log",
		SocketFile():   "/y/state/wrekin/wrekin.sock",
	}
	for got, want := range cases {
		if got != filepath.FromSlash(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestPathsDefaultToHomeDirectories(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	if !strings.HasSuffix(ConfigDir(), filepath.Join(".config", "wrekin")) {
		t.Errorf("ConfigDir() = %q, want a ~/.config/wrekin suffix", ConfigDir())
	}
	if !strings.HasSuffix(StateDir(), filepath.Join(".local", "state", "wrekin")) {
		t.Errorf("StateDir() = %q, want a ~/.local/state/wrekin suffix", StateDir())
	}
	if !filepath.IsAbs(ConfigDir()) || !filepath.IsAbs(StateDir()) {
		t.Errorf("directories must be absolute: %q %q", ConfigDir(), StateDir())
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Relay.URL != DefaultRelayURL {
		t.Errorf("relay url = %q, want %q", s.Relay.URL, DefaultRelayURL)
	}
	if s.Teleport.MaxConcurrent != 4 {
		t.Errorf("maxConcurrent = %d, want 4", s.Teleport.MaxConcurrent)
	}
	if s.Teleport.Terminal != "tmux" {
		t.Errorf("terminal = %q, want tmux", s.Teleport.Terminal)
	}
	if s.Logging.Level != "info" {
		t.Errorf("level = %q, want info", s.Logging.Level)
	}
	if !s.Workspace.AutoDiscover {
		t.Errorf("autoDiscover should default to true")
	}
	host, _ := os.Hostname()
	if s.Name == "" || !strings.HasPrefix(strings.ToLower(host), s.Name) {
		t.Errorf("name = %q, want it derived from hostname %q", s.Name, host)
	}
}

func TestLoadExpandsTildeInDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{
  "name": "box",
  "workspace": {
    "allowedDirectories": ["~/projects", "/srv/work"],
    "defaultDirectory": "~/projects",
    "autoDiscover": false
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "projects")
	if s.Workspace.AllowedDirectories[0] != want {
		t.Errorf("allowedDirectories[0] = %q, want %q", s.Workspace.AllowedDirectories[0], want)
	}
	if s.Workspace.AllowedDirectories[1] != "/srv/work" {
		t.Errorf("allowedDirectories[1] = %q, want /srv/work", s.Workspace.AllowedDirectories[1])
	}
	if s.Workspace.DefaultDirectory != want {
		t.Errorf("defaultDirectory = %q, want %q", s.Workspace.DefaultDirectory, want)
	}
	if s.Workspace.AutoDiscover {
		t.Errorf("an explicit autoDiscover:false must be kept")
	}
	if s.Name != "box" {
		t.Errorf("name = %q, want box", s.Name)
	}
	// Untouched sections keep their defaults.
	if s.Teleport.MaxConcurrent != 4 || s.Relay.URL != DefaultRelayURL {
		t.Errorf("defaults were not kept for absent sections: %+v", s)
	}
}

// The configuration directory also holds identity.json (the private key);
// whichever of config.Save and identity.Save creates it, it must be private.
func TestSaveCreatesPrivateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh", "settings.json")
	if err := Save(path, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %04o, want 0700", info.Mode().Perm())
	}
	if _, err := Load(path); err != nil {
		t.Errorf("Load after Save: %v", err)
	}
}

func TestValidateRejectsZeroMaxConcurrent(t *testing.T) {
	s := Default()
	s.Teleport.MaxConcurrent = 0
	if err := s.Validate(); err == nil {
		t.Fatal("expected an error for maxConcurrent = 0")
	}
}

func TestValidateRejectsNonWSSRelay(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"wss://wrekin.platyplus.io/connect", true},
		{"http://wrekin.platyplus.io/connect", false},
		{"https://wrekin.platyplus.io/connect", false},
		{"ws://example.com/connect", false},
		{"ws://localhost:8787/connect", true},
		{"ws://127.0.0.1:8787/connect", true},
		{"", false},
		{"not a url", false},
	}
	for _, c := range cases {
		s := Default()
		s.Relay.URL = c.url
		err := s.Validate()
		if c.ok && err != nil {
			t.Errorf("%q: unexpected error %v", c.url, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%q: expected an error", c.url)
		}
	}
}

func TestValidateRejectsUnknownTerminalAndLevel(t *testing.T) {
	s := Default()
	s.Teleport.Terminal = "screen"
	if err := s.Validate(); err == nil {
		t.Error("expected an error for an unknown terminal")
	}
	s = Default()
	s.Logging.Level = "loud"
	if err := s.Validate(); err == nil {
		t.Error("expected an error for an unknown log level")
	}
}

func TestHTTPBaseURL(t *testing.T) {
	cases := map[string]string{
		"wss://wrekin.platyplus.io/connect": "https://wrekin.platyplus.io",
		"ws://localhost:8787/connect":       "http://localhost:8787",
		"wss://example.com/relay/connect":   "https://example.com/relay",
		"wss://example.com":                 "https://example.com",
	}
	for in, want := range cases {
		s := Default()
		s.Relay.URL = in
		if got := s.HTTPBaseURL(); got != want {
			t.Errorf("HTTPBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/a/b"); got != filepath.Join(home, "a", "b") {
		t.Errorf("ExpandHome(~/a/b) = %q", got)
	}
	if got := ExpandHome("~"); got != home {
		t.Errorf("ExpandHome(~) = %q", got)
	}
	if got := ExpandHome("/abs"); got != "/abs" {
		t.Errorf("ExpandHome(/abs) = %q", got)
	}
	if got := ExpandHome("~user/x"); got != "~user/x" {
		t.Errorf("ExpandHome(~user/x) = %q, must be left alone", got)
	}
}

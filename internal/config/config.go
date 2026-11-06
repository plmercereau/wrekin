// Package config loads and validates ~/.config/wrekin/settings.json, the
// user-facing configuration described in the design specification (§3).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plmercereau/wrekin/internal/paths"
	"github.com/plmercereau/wrekin/internal/workspace"
)

// DefaultRelayURL is the production relay endpoint.
const DefaultRelayURL = "wss://wrekin.platyplus.io/connect"

// Settings mirrors settings.json key for key.
type Settings struct {
	Name      string            `json:"name"`
	Relay     RelaySettings     `json:"relay"`
	Workspace WorkspaceSettings `json:"workspace"`
	Teleport  TeleportSettings  `json:"teleport"`
	Logging   LoggingSettings   `json:"logging"`
}

// RelaySettings locates the mediator.
type RelaySettings struct {
	URL string `json:"url"`
}

// WorkspaceSettings bounds the directories a teleport may use.
type WorkspaceSettings struct {
	AllowedDirectories []string `json:"allowedDirectories"`
	DefaultDirectory   string   `json:"defaultDirectory"`
	AutoDiscover       bool     `json:"autoDiscover"`
	// Repositories pins a repository (any form workspace.ParseRepository
	// accepts) to the directory of its checkout. A pin is an explicit
	// decision: it wins over what the daemon learned, and an invalid pin fails
	// the teleport instead of being skipped.
	Repositories map[string]string `json:"repositories,omitempty"`
}

// TeleportSettings tunes how sessions are launched.
type TeleportSettings struct {
	Terminal      string `json:"terminal"`
	MaxConcurrent int    `json:"maxConcurrent"`
}

// LoggingSettings controls the local log.
type LoggingSettings struct {
	Level string `json:"level"`
}

// Default returns the settings used when the file is absent. Directories are
// kept with their leading "~" so that the struct can be written back as a
// portable settings.json; Load expands them.
func Default() Settings {
	return Settings{
		Name:  defaultName(),
		Relay: RelaySettings{URL: DefaultRelayURL},
		Workspace: WorkspaceSettings{
			AllowedDirectories: []string{"~/projects"},
			DefaultDirectory:   "~/projects",
			AutoDiscover:       true,
		},
		Teleport: TeleportSettings{Terminal: "tmux", MaxConcurrent: 4},
		Logging:  LoggingSettings{Level: "info"},
	}
}

// Load reads the settings file at path. A missing file yields Default().
// Absent keys keep their default value, "~" is expanded in directories and
// the result is validated.
func Load(path string) (Settings, error) {
	s := Default()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Defaults only.
	case err != nil:
		return Settings{}, fmt.Errorf("read settings: %w", err)
	default:
		if err := json.Unmarshal(data, &s); err != nil {
			return Settings{}, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if s.Name == "" {
		s.Name = defaultName()
	}
	for i, d := range s.Workspace.AllowedDirectories {
		s.Workspace.AllowedDirectories[i] = filepath.Clean(ExpandHome(d))
	}
	if s.Workspace.DefaultDirectory == "" && len(s.Workspace.AllowedDirectories) > 0 {
		s.Workspace.DefaultDirectory = s.Workspace.AllowedDirectories[0]
	}
	s.Workspace.DefaultDirectory = filepath.Clean(ExpandHome(s.Workspace.DefaultDirectory))
	for repo, dir := range s.Workspace.Repositories {
		if strings.TrimSpace(dir) != "" {
			s.Workspace.Repositories[repo] = filepath.Clean(ExpandHome(dir))
		}
	}
	if err := s.Validate(); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Save writes the settings as indented JSON, creating the directory private
// (0700): it is also where identity.Save keeps the node's private key, and
// whichever of the two runs first must not leave it wider.
func Save(path string, s Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Validate checks the invariants the daemon relies on.
func (s Settings) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name must not be empty")
	}
	if err := validateRelayURL(s.Relay.URL); err != nil {
		return err
	}
	if len(s.Workspace.AllowedDirectories) == 0 {
		return errors.New("workspace.allowedDirectories must list at least one directory")
	}
	for _, d := range s.Workspace.AllowedDirectories {
		if d == "" {
			return errors.New("workspace.allowedDirectories must not contain an empty entry")
		}
	}
	if s.Workspace.DefaultDirectory == "" {
		return errors.New("workspace.defaultDirectory must be set")
	}
	if err := validateRepositories(s.Workspace.Repositories); err != nil {
		return err
	}
	if s.Teleport.Terminal != "tmux" {
		return fmt.Errorf("teleport.terminal %q is not supported (only \"tmux\")", s.Teleport.Terminal)
	}
	if s.Teleport.MaxConcurrent < 1 {
		return fmt.Errorf("teleport.maxConcurrent must be at least 1, got %d", s.Teleport.MaxConcurrent)
	}
	switch s.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level %q is not one of debug, info, warn, error", s.Logging.Level)
	}
	return nil
}

// validateRepositories checks the declared mappings: every key is a
// repository reference, every value a directory, and no repository is
// declared twice under two spellings.
func validateRepositories(repos map[string]string) error {
	keys := make([]string, 0, len(repos))
	for repo := range repos {
		keys = append(keys, repo)
	}
	sort.Strings(keys)
	seen := map[string]string{}
	for _, repo := range keys {
		if _, err := workspace.ParseRepository(repo); err != nil {
			return fmt.Errorf("workspace.repositories: %q: %w", repo, err)
		}
		if strings.TrimSpace(repos[repo]) == "" {
			return fmt.Errorf("workspace.repositories: %q has an empty directory", repo)
		}
		key, _ := workspace.RepoKey(repo)
		if other, dup := seen[key]; dup {
			return fmt.Errorf("workspace.repositories: %q and %q name the same repository", other, repo)
		}
		seen[key] = repo
	}
	return nil
}

func validateRelayURL(raw string) error {
	if raw == "" {
		return errors.New("relay.url must be set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("relay.url: %w", err)
	}
	if u.Host == "" {
		return fmt.Errorf("relay.url %q has no host", raw)
	}
	switch u.Scheme {
	case "wss":
		return nil
	case "ws":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("relay.url %q: plain ws:// is only allowed for localhost", raw)
	default:
		return fmt.Errorf("relay.url %q must use the wss:// scheme", raw)
	}
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// HTTPBaseURL derives the cloud HTTP base URL from the relay URL:
// wss://host/connect becomes https://host.
func (s Settings) HTTPBaseURL() string {
	u, err := url.Parse(s.Relay.URL)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/connect")
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// ExpandHome replaces a leading "~" or "~/" with the user's home directory.
// Other forms ("~user") are left alone.
func ExpandHome(p string) string { return paths.ExpandHome(p) }

// defaultName derives a node name from the hostname: lower-case, without the
// domain part.
func defaultName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "node"
	}
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	return host
}

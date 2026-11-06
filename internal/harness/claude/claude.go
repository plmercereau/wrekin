// Package claude is the Claude Code harness adapter: the only place that
// knows the claude binary, its --teleport flag, its prompt and ~/.claude.json.
package claude

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/plmercereau/wrekin/internal/harness"
)

// ID is the protocol harness id.
const ID = "claude-code"

// Claude adapts Claude Code.
type Claude struct {
	claudeJSON string
	binary     string

	mu       sync.Mutex
	resolved string
}

// New builds the adapter. claudeJSONPath is ~/.claude.json (see
// DefaultClaudeJSONPath); binary is the executable name or path.
func New(claudeJSONPath, binary string) *Claude {
	if binary == "" {
		binary = "claude"
	}
	if claudeJSONPath == "" {
		claudeJSONPath = DefaultClaudeJSONPath()
	}
	return &Claude{claudeJSON: claudeJSONPath, binary: binary}
}

// DefaultClaudeJSONPath is ~/.claude.json.
func DefaultClaudeJSONPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude.json"
	}
	return filepath.Join(home, ".claude.json")
}

// ID implements harness.Harness.
func (c *Claude) ID() string { return ID }

// Detect implements harness.Harness by running `claude --version`.
func (c *Claude) Detect() (harness.Info, error) {
	path, err := exec.LookPath(c.binary)
	if err != nil {
		return harness.Info{}, nil
	}
	c.mu.Lock()
	c.resolved = path
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// The binary exists but did not answer; still report it as present.
		return harness.Info{Available: true}, nil
	}
	return harness.Info{Available: true, Version: parseVersion(out.String())}, nil
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+[\w.-]*`)

func parseVersion(out string) string {
	return versionRe.FindString(strings.TrimSpace(out))
}

// cloudSessionIDRe is the shape of a Claude Code Cloud session id. The
// `--teleport` option takes an optional value, so a token starting with "-"
// would be read as another flag (`--dangerously-skip-permissions` among
// them): only ids of this shape are ever placed after it.
var cloudSessionIDRe = regexp.MustCompile(`^session_[A-Za-z0-9]{1,190}$`)

// LaunchArgs implements harness.Harness. The binary is the absolute path
// found by Detect when available, so launches do not depend on the PATH of
// the terminal's shell. It returns nil for an id that is not a session id.
func (c *Claude) LaunchArgs(cloudSessionID string) []string {
	if !cloudSessionIDRe.MatchString(cloudSessionID) {
		return nil
	}
	c.mu.Lock()
	bin := c.resolved
	c.mu.Unlock()
	if bin == "" {
		bin = c.binary
	}
	return []string{bin, "--teleport", cloudSessionID}
}

// readyPattern matches the Claude Code prompt: the shortcuts hint under the
// input box, the "❯" prompt, or a line that ends with the ">" prompt.
var readyPattern = regexp.MustCompile(`(?m)\? for shortcuts|❯|>\s*$`)

// ReadyPattern implements harness.Harness.
func (c *Claude) ReadyPattern() *regexp.Regexp { return readyPattern }

// ExitedIsFailure implements harness.Harness.
func (c *Claude) ExitedIsFailure() bool { return true }

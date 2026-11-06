// Package claude is the Claude Code harness adapter: the only place that
// knows the claude binary, its --teleport flag, its prompt and ~/.claude.json.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// DefaultClaudeJSONPath is ~/.claude.json, or .claude.json in
// $CLAUDE_CONFIG_DIR when the daemon runs with Claude Code's configuration
// moved there, as Claude Code itself reads it.
func DefaultClaudeJSONPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
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

// SignedIn implements harness.Harness with `claude auth status --json`, which
// prints {"loggedIn": …, "authMethod": …} and exits 1 when signed out. It
// sees what a launch would see: the daemon's environment
// (ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN) as well as the stored
// credentials. A version without that command answers something else, and
// the result is then an error: the caller cannot tell and goes on.
func (c *Claude) SignedIn(ctx context.Context) (bool, error) {
	out, err := c.authStatus(ctx)
	if err != nil {
		return false, err
	}
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if err := json.Unmarshal(out, &status); err != nil || status.LoggedIn == nil {
		return false, fmt.Errorf("claude auth status: unexpected output %q", truncate(string(out), 200))
	}
	return *status.LoggedIn, nil
}

// authStatus runs `claude auth status --json` and returns its JSON output,
// whatever its exit status (1 when signed out), or an error when it printed
// no JSON object.
func (c *Claude) authStatus(ctx context.Context) ([]byte, error) {
	path, err := exec.LookPath(c.binary)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", exec.ErrNotFound, c.binary)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "auth", "status", "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	runErr := cmd.Run()
	out := bytes.TrimSpace(stdout.Bytes())
	if !bytes.HasPrefix(out, []byte("{")) {
		if runErr != nil {
			return nil, fmt.Errorf("claude auth status: %w", runErr)
		}
		return nil, fmt.Errorf("claude auth status: unexpected output %q", truncate(string(out), 200))
	}
	return out, nil
}

// Login runs `claude auth login` on the caller's terminal: it prints a URL,
// waits for the browser, and stores the credentials where every later
// `claude` of this user finds them, the daemon's included.
func (c *Claude) Login(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	path, err := exec.LookPath(c.binary)
	if err != nil {
		return fmt.Errorf("%w: %s", exec.ErrNotFound, c.binary)
	}
	cmd := exec.CommandContext(ctx, path, "auth", "login")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
//
// Remote control is not asked for here: Claude Code (2.1.177 and 2.1.316,
// probed on a node) ignores --remote-control next to --teleport. It is
// turned on once the session is up, with RemoteControlCommand.
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

// remoteControlNameRe bounds the Remote Control name typed into the session:
// one line of words and common punctuation, starting with a letter or a
// digit, nothing the input box could read as anything else (no control
// character, so no Enter in it).
var remoteControlNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._,:;'’!?()&+/#-]{0,99}$`)

// RemoteControlCommand implements harness.Harness with Claude Code's
// /remote-control, which takes the session's name and, once its consent has
// been given (remoteDialogSeen), turns Remote Control on at once.
func (c *Claude) RemoteControlCommand(name string) string {
	if !remoteControlNameRe.MatchString(name) {
		return ""
	}
	return "/remote-control " + name
}

// The status line of a session under Remote Control ("/rc active", bottom
// right) and the line /remote-control prints: "/remote-control is active ·
// Continue here, on your phone, or at https://claude.ai/code/session_…".
var (
	remoteControlActive = regexp.MustCompile(`/rc active|/remote-control is active|Remote Control active`)
	remoteControlURL    = regexp.MustCompile(`https://claude\.ai/code/session_[A-Za-z0-9]+`)
)

// RemoteControl implements harness.Harness. The URL is the last one printed,
// the current session's.
func (c *Claude) RemoteControl(pane string) (bool, string) {
	if !remoteControlActive.MatchString(pane) {
		return false, ""
	}
	urls := remoteControlURL.FindAllString(pane, -1)
	if len(urls) == 0 {
		return true, ""
	}
	return true, urls[len(urls)-1]
}

// InputIdle implements harness.Harness. Claude Code draws its input box as a
// "❯" line between two horizontal rules; a dialog in front of it (an
// upsell, a menu) replaces that line with its own content, where "❯" marks
// a numbered choice. Typing into a dialog would answer it, so only the bare
// box counts.
func (c *Claude) InputIdle(pane string) bool {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	for i := len(lines) - 2; i >= 1; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "❯") || menuLine.MatchString(line) {
			continue
		}
		return isRule(lines[i-1]) && isRule(lines[i+1])
	}
	return false
}

var menuLine = regexp.MustCompile(`^❯\s*\d+\.`)

func isRule(line string) bool {
	line = strings.TrimSpace(line)
	return line != "" && strings.Trim(line, "─") == ""
}

// readyPattern matches the Claude Code input prompt: the shortcuts hint
// under the input box, or a line that starts (inside the box's border, if
// any) with "❯" or ">" followed by nothing or by text. A menu line such as
// "❯ 2. Dark mode" (the first-run theme picker, the trust and login dialogs)
// is not a prompt: the marker is followed by a number there. So is a ">" at
// the end of a line ("Paste code here if prompted >").
var readyPattern = regexp.MustCompile(`(?m)\? for shortcuts|^[ \t│]*[❯>](?:[ \t\x{a0}]*$|[ \t\x{a0}]+[^\d\s])`)

// ReadyPattern implements harness.Harness.
func (c *Claude) ReadyPattern() *regexp.Regexp { return readyPattern }

// setupScreens are the interactive screens Claude Code shows before it opens
// any session, and that no one answers on a node until the user attaches.
var setupScreens = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`Choose the text style`), "its first-run setup (the theme choice)"},
	{regexp.MustCompile(`Select login method|Paste code here if prompted`), "its sign-in"},
	{regexp.MustCompile(`(?i)do you trust the files in this folder|is this a project you created or one you trust|trust this folder`), "the trust dialog for this folder"},
	{regexp.MustCompile(`Enable Remote Control`), "the Remote Control consent"},
}

// SetupScreen implements harness.Harness.
func (c *Claude) SetupScreen(pane string) string {
	for _, s := range setupScreens {
		if s.re.MatchString(pane) {
			return s.what
		}
	}
	return ""
}

// ExitReason implements harness.Harness: the last lines Claude Code printed,
// and, when it could not find the session, which account this machine's
// Claude Code is signed in with. A cloud session can only be teleported by
// the Claude account that owns it, and a machine signed in with another one
// (a work account next to a personal one) gets "Session not found".
func (c *Claude) ExitReason(ctx context.Context, pane string) string {
	var said []string
	for _, line := range strings.Split(pane, "\n") {
		line = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r < ' ' || r == 0x7f {
				return -1
			}
			return r
		}, line))
		if line == "" || strings.HasPrefix(line, "Pane is dead") {
			continue
		}
		said = append(said, line)
	}
	if len(said) > 3 {
		said = said[len(said)-3:]
	}
	reason := truncate(strings.Join(said, " / "), 300)
	if reason == "" {
		return ""
	}
	reason = "Claude Code said: " + reason
	if strings.Contains(reason, "Session not found") {
		if email, org, err := c.account(ctx); err == nil && email != "" {
			who := email
			if org != "" {
				who += " (" + org + ")"
			}
			reason += ". Claude Code on this machine is signed in as " + who +
				": a cloud session can only be teleported by the Claude account that owns it; sign in there with that account (`claude auth login`)"
		}
	}
	return reason
}

// account reads the signed-in Claude account from `claude auth status --json`.
func (c *Claude) account(ctx context.Context) (email, org string, err error) {
	out, err := c.authStatus(ctx)
	if err != nil {
		return "", "", err
	}
	var status struct {
		Email   string `json:"email"`
		OrgName string `json:"orgName"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", "", err
	}
	return status.Email, status.OrgName, nil
}

// ExitedIsFailure implements harness.Harness.
func (c *Claude) ExitedIsFailure() bool { return true }

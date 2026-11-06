package claude

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plmercereau/wrekin/internal/harness"
)

func TestLaunchArgs(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), ".claude.json"), "/opt/bin/claude")
	got := c.LaunchArgs("session_01CN", harness.LaunchOptions{})
	want := []string{"/opt/bin/claude", "--teleport", "session_01CN"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("LaunchArgs = %v, want %v", got, want)
	}
	for _, bad := range []string{"--dangerously-skip-permissions", "-r", "", "session_01 CN", "abc", "session_x;rm"} {
		if got := c.LaunchArgs(bad, harness.LaunchOptions{}); got != nil {
			t.Errorf("LaunchArgs(%q) = %v, want nil: a flag-shaped id must never reach the harness", bad, got)
		}
	}
	if c.ID() != ID || ID != "claude-code" {
		t.Errorf("ID = %q", c.ID())
	}
	if !c.ExitedIsFailure() {
		t.Error("an exited harness is a failure")
	}
}

func TestReadyPatternMatchesPromptLines(t *testing.T) {
	re := New("", "claude").ReadyPattern()
	for _, pane := range []string{
		"Welcome back\n\n> \n\n  ? for shortcuts",
		"╭──────╮\n│ > Try \"fix the bug\" │\n╰──────╯\n  ? for shortcuts",
		"❯ ",
		"some banner\n>",
		"────────\n❯ \n────────",
		"❯ téléporte cette session",
	} {
		if !re.MatchString(pane) {
			t.Errorf("pattern should match prompt pane:\n%s", pane)
		}
	}
	for _, pane := range []string{
		"Cloning into 'wrekin'...",
		"Connecting to session...",
		"error: --teleport requires a valid session id",
		"a -> b arrow in the middle of a line",
		"prompt line >   ",
		// Claude Code 2.1.177's first-run screen, captured on a node: its menu
		// marker was taken for the prompt and the job reported ready.
		" Choose the text style that looks best with your terminal\n   1. Auto (match terminal)\n ❯ 2. Dark mode ✔\n   3. Light mode",
		" ❯ 1. Yes, I trust this folder\n   2. No, exit",
		"Paste code here if prompted >",
	} {
		if re.MatchString(pane) {
			t.Errorf("pattern must not match:\n%s", pane)
		}
	}
}

func TestDetectMissingBinaryIsUnavailable(t *testing.T) {
	c := New("", filepath.Join(t.TempDir(), "no-such-claude"))
	info, err := c.Detect()
	if err != nil {
		t.Fatalf("a missing binary is not an error: %v", err)
	}
	if info.Available {
		t.Error("missing binary reported available")
	}
}

func TestDetectParsesVersion(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	writeExecutable(t, fake, "#!/bin/sh\necho '2.1.318 (Claude Code)'\n")
	c := New("", fake)
	info, err := c.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !info.Available || info.Version != "2.1.318" {
		t.Errorf("info = %+v", info)
	}
	if got := c.LaunchArgs("session_s", harness.LaunchOptions{})[0]; got != fake {
		t.Errorf("LaunchArgs binary = %q, want the resolved %q", got, fake)
	}
}

func TestSignedInReadsAuthStatus(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, script string
		want         bool
		wantErr      bool
	}{
		// What Claude Code 2.1.177 prints, and its exit status 1, when signed out.
		{"signed out", `echo '{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}'; exit 1`, false, false},
		{"signed in", `echo '{"loggedIn": true, "authMethod": "claude.ai"}'`, true, false},
		{"no auth command", `echo "error: unknown command 'auth'" >&2; exit 1`, false, true},
		{"unexpected output", `echo 'hello'`, false, true},
	}
	for i, tc := range cases {
		fake := filepath.Join(dir, fmt.Sprintf("claude-%d", i))
		writeExecutable(t, fake, "#!/bin/sh\n"+tc.script+"\n")
		got, err := New("", fake).SignedIn(context.Background())
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%s: SignedIn = %v, %v", tc.name, got, err)
		}
	}
	if _, err := New("", filepath.Join(dir, "absent")).SignedIn(context.Background()); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("a missing binary: err = %v, want exec.ErrNotFound", err)
	}
}

func TestSetupScreenNamesWhatClaudeWaitsOn(t *testing.T) {
	c := New("", "claude")
	for pane, want := range map[string]string{
		" Let's get started.\n Choose the text style that looks best with your terminal\n ❯ 2. Dark mode ✔": "its first-run setup (the theme choice)",
		"Select login method:\n ❯ 1. Claude account with subscription":                                      "its sign-in",
		"Do you trust the files in this folder?\n ❯ 1. Yes, proceed":                                        "the trust dialog for this folder",
		"────────\n❯ \n────────\n  ? for shortcuts":                                                         "",
	} {
		if got := c.SetupScreen(pane); got != want {
			t.Errorf("SetupScreen(%q) = %q, want %q", pane, got, want)
		}
	}
}

func TestLaunchArgsWithRemoteControl(t *testing.T) {
	c := New("", "/opt/bin/claude")
	got := c.LaunchArgs("session_01CN", harness.LaunchOptions{RemoteControl: "fennec wrekin-ydncipcr"})
	want := []string{"/opt/bin/claude", "--teleport", "session_01CN", "--remote-control", "fennec wrekin-ydncipcr"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %q, want %q", got, want)
	}
	// --remote-control takes an optional value: a name shaped like a flag would be read as one.
	for _, bad := range []string{"--dangerously-skip-permissions", "-x", " lead", "a;b", ""} {
		args := c.LaunchArgs("session_01CN", harness.LaunchOptions{RemoteControl: bad})
		if bad == "" {
			if len(args) != 3 {
				t.Errorf("no name must mean no remote control: %q", args)
			}
			continue
		}
		if args != nil {
			t.Errorf("name %q accepted: %q", bad, args)
		}
	}
}

func TestRemoteControlActiveReadsTheStatusLine(t *testing.T) {
	c := New("", "claude")
	// The bottom of a Claude Code 2.1.177 pane once /remote-control is on, captured on fennec.
	pane := "────────\n❯ \n────────\n  ? for shortcuts · ← for agents      Now using usage credits\n                    ● high · /effort\n                          /rc active"
	if !c.RemoteControlActive(pane) {
		t.Error("/rc active not recognised")
	}
	if c.RemoteControlActive("────────\n❯ \n────────\n  ? for shortcuts") {
		t.Error("a pane without it reported active")
	}
	if got := c.SetupScreen(" Remote Control\n   ❯ 1. Enable Remote Control  Opens a secure connection to claude.ai."); got != "the Remote Control consent" {
		t.Errorf("consent screen = %q", got)
	}
}

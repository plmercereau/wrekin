package claude

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchArgs(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), ".claude.json"), "/opt/bin/claude")
	got := c.LaunchArgs("session_01CN")
	want := []string{"/opt/bin/claude", "--teleport", "session_01CN"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("LaunchArgs = %v, want %v", got, want)
	}
	for _, bad := range []string{"--dangerously-skip-permissions", "-r", "", "session_01 CN", "abc", "session_x;rm"} {
		if got := c.LaunchArgs(bad); got != nil {
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
		"prompt line >   ",
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
	if got := c.LaunchArgs("session_s")[0]; got != fake {
		t.Errorf("LaunchArgs binary = %q, want the resolved %q", got, fake)
	}
}

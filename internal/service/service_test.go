package service

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnitContent(t *testing.T) {
	unit := SystemdUnit("/usr/local/bin/wrekin")
	for _, want := range []string{
		"[Unit]", "[Service]", "[Install]",
		"ExecStart=/usr/local/bin/wrekin daemon",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "/x")
	if got := systemdUnitPath(); got != filepath.FromSlash("/x/systemd/user/wrekin.service") {
		t.Errorf("unit path = %q", got)
	}
}

func TestLaunchdPlistContent(t *testing.T) {
	plist := LaunchdPlist("/opt/homebrew/bin/wrekin", "/Users/me/.local/state/wrekin/launchd.log")
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<key>Label</key>", "<string>io.platyplus.wrekin</string>",
		"<key>ProgramArguments</key>", "<string>/opt/homebrew/bin/wrekin</string>", "<string>daemon</string>",
		"<key>RunAtLoad</key>", "<true/>",
		"<key>KeepAlive</key>",
		"<key>StandardOutPath</key>", "<string>/Users/me/.local/state/wrekin/launchd.log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	if !strings.HasSuffix(launchdPlistPath(), filepath.Join("Library", "LaunchAgents", "io.platyplus.wrekin.plist")) {
		t.Errorf("plist path = %q", launchdPlistPath())
	}
	escaped := LaunchdPlist("/a&b/wrekin", "/log")
	if !strings.Contains(escaped, "/a&amp;b/wrekin") {
		t.Error("plist strings must be XML-escaped")
	}
}

func TestNoneManagerExplains(t *testing.T) {
	m := none{}
	if m.Name() != "none" {
		t.Errorf("Name = %q", m.Name())
	}
	if err := m.Install("/x"); err == nil || !strings.Contains(err.Error(), "--foreground") {
		t.Errorf("Install err = %v", err)
	}
	st, err := m.Status()
	if err != nil || st.Installed || st.Running {
		t.Errorf("Status = %+v %v", st, err)
	}
}

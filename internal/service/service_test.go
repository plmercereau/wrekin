package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnitContent(t *testing.T) {
	unit := SystemdUnit(Spec{Binary: "/usr/local/bin/wrekin", Path: "/home/me/.local/bin:/usr/bin:/bin"})
	for _, want := range []string{
		"[Unit]", "[Service]", "[Install]",
		"ExecStart=/usr/local/bin/wrekin daemon",
		`Environment="PATH=/home/me/.local/bin:/usr/bin:/bin"`,
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
	plist := LaunchdPlist(Spec{Binary: "/opt/homebrew/bin/wrekin", Path: "/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin"}, "/Users/me/.local/state/wrekin/launchd.log")
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<key>EnvironmentVariables</key>", "<key>PATH</key>", "<string>/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>",
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
	escaped := LaunchdPlist(Spec{Binary: "/a&b/wrekin", Path: "/p"}, "/log")
	if !strings.Contains(escaped, "/a&amp;b/wrekin") {
		t.Error("plist strings must be XML-escaped")
	}
}

func TestNoneManagerExplains(t *testing.T) {
	m := none{}
	if m.Name() != "none" {
		t.Errorf("Name = %q", m.Name())
	}
	if err := m.Install(Spec{Binary: "/x"}); err == nil || !strings.Contains(err.Error(), "--foreground") {
		t.Errorf("Install err = %v", err)
	}
	st, err := m.Status()
	if err != nil || st.Installed || st.Running {
		t.Errorf("Status = %+v %v", st, err)
	}
}

func TestServicePathPutsLocalBinFirstAndDeduplicates(t *testing.T) {
	got := servicePath("/usr/bin:/home/me/.local/bin:/opt/x/bin:/usr/bin", "/home/me", "linux")
	want := "/home/me/.local/bin:/usr/bin:/opt/x/bin:/usr/local/bin:/bin"
	if got != want {
		t.Errorf("servicePath =\n %s\nwant\n %s", got, want)
	}
	mac := servicePath("", "/Users/me", "darwin")
	if !strings.HasPrefix(mac, "/Users/me/.local/bin:/opt/homebrew/bin:/usr/local/bin:") {
		t.Errorf("darwin servicePath = %s", mac)
	}
}

func TestBinaryPathPrefersTheInvokedWrekin(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "wrekin")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })

	os.Args = []string{wrapper}
	if got, err := BinaryPath(); err != nil || got != wrapper {
		t.Errorf("absolute argv[0]: got %q %v, want %q", got, err, wrapper)
	}
	t.Setenv("PATH", dir)
	os.Args = []string{"wrekin"}
	if got, err := BinaryPath(); err != nil || got != wrapper {
		t.Errorf("argv[0] from PATH: got %q %v, want %q", got, err, wrapper)
	}
	// Anything that is not a wrekin binary (a test binary, a renamed copy) falls back to the executable.
	os.Args = []string{"not-wrekin"}
	exe, _ := os.Executable()
	if got, err := BinaryPath(); err != nil || got != exe {
		t.Errorf("fallback: got %q %v, want %q", got, err, exe)
	}
}

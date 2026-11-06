package service

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/plmercereau/wrekin/internal/paths"
)

const launchdLabel = "io.platyplus.wrekin"

// LaunchdPlist renders the LaunchAgent for the given spec and log file.
func LaunchdPlist(spec Spec, logPath string) string {
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	// AbandonProcessGroup: the teleported sessions live in a tmux server the
	// daemon starts; launchd must not take it down with the daemon.
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, launchdLabel, esc(spec.Binary), esc(spec.Path), esc(logPath), esc(logPath))
}

func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func launchdLogPath() string { return filepath.Join(paths.StateDir(), "launchd.log") }

type launchd struct{}

func (launchd) Name() string { return "launchd" }

func (launchd) domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func (l launchd) run(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (l launchd) Install(spec Spec) error {
	path := launchdPlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.StateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(LaunchdPlist(spec, launchdLogPath())), 0o644)
}

// Start loads the agent; RunAtLoad starts the daemon.
func (l launchd) Start() error {
	if err := l.run("bootstrap", l.domain(), launchdPlistPath()); err != nil {
		// Already loaded: make sure it runs.
		if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "37") || strings.Contains(err.Error(), "5:") {
			return l.run("kickstart", l.domain()+"/"+launchdLabel)
		}
		return err
	}
	return nil
}

// Stop unloads the agent, which stops the daemon.
func (l launchd) Stop() error {
	return l.run("bootout", l.domain()+"/"+launchdLabel)
}

func (l launchd) Status() (Status, error) {
	if _, err := os.Stat(launchdPlistPath()); err != nil {
		return Status{}, nil
	}
	out, err := exec.Command("launchctl", "print", l.domain()+"/"+launchdLabel).Output()
	if err != nil {
		return Status{Installed: true, Detail: "not loaded"}, nil
	}
	running := strings.Contains(string(out), "state = running")
	return Status{Installed: true, Running: running, Detail: "loaded"}, nil
}

func (l launchd) Uninstall() error {
	_ = l.Stop()
	if err := os.Remove(launchdPlistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

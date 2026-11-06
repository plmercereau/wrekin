package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const systemdUnitName = "wrekin.service"

// SystemdUnit renders the user unit for the given binary.
func SystemdUnit(binary string) string {
	return fmt.Sprintf(`[Unit]
Description=Wrekin node agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, binary)
}

func systemdUnitPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", systemdUnitName)
}

type systemd struct{}

func (systemd) Name() string { return "systemd" }

func (s systemd) run(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s systemd) Install(binary string) error {
	path := systemdUnitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(SystemdUnit(binary)), 0o644); err != nil {
		return err
	}
	if err := s.run("daemon-reload"); err != nil {
		return err
	}
	return s.run("enable", systemdUnitName)
}

func (s systemd) Start() error { return s.run("start", systemdUnitName) }
func (s systemd) Stop() error  { return s.run("stop", systemdUnitName) }

func (s systemd) Status() (Status, error) {
	if _, err := os.Stat(systemdUnitPath()); err != nil {
		return Status{}, nil
	}
	out, _ := exec.Command("systemctl", "--user", "is-active", systemdUnitName).Output()
	state := strings.TrimSpace(string(out))
	return Status{Installed: true, Running: state == "active", Detail: state}, nil
}

func (s systemd) Uninstall() error {
	_ = s.run("disable", "--now", systemdUnitName)
	if err := os.Remove(systemdUnitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return s.run("daemon-reload")
}

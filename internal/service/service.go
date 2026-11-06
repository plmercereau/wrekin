// Package service installs the daemon as a user service: systemd --user on
// Linux, a launchd LaunchAgent on macOS.
package service

import (
	"errors"
	"os/exec"
	"runtime"
)

// Status of the installed service.
type Status struct {
	Installed bool
	Running   bool
	Detail    string
}

// Manager controls one service implementation.
type Manager interface {
	Name() string
	Install(spec Spec) error
	Start() error
	Stop() error
	Status() (Status, error)
	Uninstall() error
}

// Detect picks the manager for this machine.
func Detect() Manager {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("launchctl"); err == nil {
			return &launchd{}
		}
		return none{}
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		if err := exec.Command("systemctl", "--user", "show-environment").Run(); err == nil {
			return &systemd{}
		}
	}
	return none{}
}

// none is the fallback when no service manager is usable.
type none struct{}

var errNoManager = errors.New("no user service manager found; run `wrekin start --foreground` instead")

func (none) Name() string            { return "none" }
func (none) Install(Spec) error      { return errNoManager }
func (none) Start() error            { return errNoManager }
func (none) Stop() error             { return errNoManager }
func (none) Status() (Status, error) { return Status{}, nil }
func (none) Uninstall() error        { return nil }

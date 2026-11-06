package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Spec is what a service definition needs: the binary to run and the PATH it
// runs with. Service managers start the daemon with a minimal environment
// (launchd: /usr/bin:/bin:/usr/sbin:/sbin; systemd --user: a short default),
// in which git, tmux and claude are not found, so the PATH is recorded at
// install time from the shell that runs `wrekin start`.
type Spec struct {
	Binary string
	Path   string
}

// BinaryPath is the program the service should execute. The path the user
// invoked is preferred over os.Executable(): a Nix wrapper, a Homebrew or a
// profile symlink stays valid across upgrades and keeps the wrapper's own
// environment, while the resolved real binary (`.wrekin-wrapped`, a Cellar
// path) changes with every release.
func BinaryPath() (string, error) {
	if invoked := invokedBinary(); invoked != "" {
		return invoked, nil
	}
	return os.Executable()
}

func invokedBinary() string {
	if len(os.Args) == 0 || os.Args[0] == "" {
		return ""
	}
	arg := os.Args[0]
	var candidate string
	if strings.ContainsRune(arg, filepath.Separator) {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return ""
		}
		candidate = abs
	} else {
		found, err := exec.LookPath(arg)
		if err != nil {
			return ""
		}
		candidate = found
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return ""
	}
	if filepath.Base(candidate) != "wrekin" {
		return ""
	}
	return candidate
}

// ServicePath builds the PATH for the service from the current environment:
// ~/.local/bin first (where Claude Code's native installer puts `claude`),
// then the PATH of the shell running `wrekin start`, then the usual system
// directories, each once.
func ServicePath() string {
	return servicePath(os.Getenv("PATH"), homeDir(), runtime.GOOS)
}

func servicePath(current, home, goos string) string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	if home != "" {
		add(filepath.Join(home, ".local", "bin"))
	}
	for _, p := range strings.Split(current, string(os.PathListSeparator)) {
		add(p)
	}
	if goos == "darwin" {
		add("/opt/homebrew/bin")
		add("/usr/local/bin")
	}
	for _, p := range []string{"/usr/local/bin", "/usr/bin", "/bin"} {
		add(p)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/paths"
	"github.com/plmercereau/wrekin/internal/repos"
)

func gitInit(t *testing.T, dir, origin string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// reposFixture sets up a home with ~/projects, a settings file pinning
// o/pinned, and learned mappings for o/found (valid), o/gone (stale) and
// o/pinned (shadowed by the pin). It returns the settings path and the home.
func reposFixture(t *testing.T) (settings, home string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not installed")
	}
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	projects := filepath.Join(home, "projects")
	gitInit(t, filepath.Join(projects, "pinned"), "git@github.com:o/pinned.git")
	gitInit(t, filepath.Join(projects, "found"), "https://github.com/o/found")
	gitInit(t, filepath.Join(projects, "old-pinned"), "https://github.com/o/pinned")
	settings = filepath.Join(home, "settings.json")
	body := `{"workspace": {"allowedDirectories": ["~/projects"], "repositories": {"o/pinned": "~/projects/pinned"}}}`
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	learned := repos.New(paths.RepositoriesFile())
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for repo, dir := range map[string]string{
		"https://github.com/o/found":  filepath.Join(projects, "found"),
		"https://github.com/o/gone":   filepath.Join(projects, "gone"),
		"https://github.com/o/pinned": filepath.Join(projects, "old-pinned"),
	} {
		if err := learned.Record(repo, dir, repos.SourceDiscovered, now); err != nil {
			t.Fatal(err)
		}
	}
	return settings, home
}

func TestReposList(t *testing.T) {
	settings, home := reposFixture(t)
	for _, args := range [][]string{{"repos"}, {"repos", "list"}} {
		out, err := run(t, append(args, "--settings", settings)...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		for _, want := range []string{"o/pinned", "declared", "~/projects/pinned", "https://github.com/o/found", "discovered", "https://github.com/o/gone", "does not exist", "shadowed"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v output lacks %q:\n%s", args, want, out)
			}
		}
	}

	out, err := run(t, "repos", "list", "--json", "--settings", settings)
	if err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	var list []struct {
		Repository string `json:"repository"`
		Key        string `json:"key"`
		Path       string `json:"path"`
		Source     string `json:"source"`
		Valid      bool   `json:"valid"`
		Problem    string `json:"problem"`
		Shadowed   bool   `json:"shadowed"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, m := range list {
		got[m.Source+" "+m.Key] = m.Path
		switch m.Key {
		case "github.com/o/gone":
			if m.Valid || m.Problem == "" {
				t.Errorf("stale mapping reported valid: %+v", m)
			}
		case "github.com/o/found":
			if !m.Valid {
				t.Errorf("valid mapping reported invalid: %+v", m)
			}
		case "github.com/o/pinned":
			if m.Source == "declared" && (!m.Valid || m.Shadowed) {
				t.Errorf("declared pin: %+v", m)
			}
			if m.Source != "declared" && !m.Shadowed {
				t.Errorf("learned entry under a pin must be shadowed: %+v", m)
			}
		}
	}
	want := map[string]string{
		"declared github.com/o/pinned":   filepath.Join(home, "projects", "pinned"),
		"discovered github.com/o/found":  filepath.Join(home, "projects", "found"),
		"discovered github.com/o/gone":   filepath.Join(home, "projects", "gone"),
		"discovered github.com/o/pinned": filepath.Join(home, "projects", "old-pinned"),
	}
	if len(got) != len(want) {
		t.Errorf("mappings = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s -> %q, want %q", k, got[k], v)
		}
	}
}

func TestReposForget(t *testing.T) {
	settings, _ := reposFixture(t)
	out, err := run(t, "repos", "forget", "git@github.com:o/found.git", "--settings", settings)
	if err != nil || !strings.Contains(out, "o/found") {
		t.Fatalf("forget: %v\n%s", err, out)
	}
	if _, ok, _ := repos.New(paths.RepositoriesFile()).Lookup("o/found"); ok {
		t.Error("the learned mapping must be gone")
	}

	out, err = run(t, "repos", "forget", "o/pinned", "--settings", settings)
	if err == nil || !strings.Contains(out+err.Error(), "settings.json") || !strings.Contains(out+err.Error(), "workspace.repositories") {
		t.Errorf("forgetting a pin must be refused with a pointer to the settings: %v\n%s", err, out)
	}
	if _, ok, _ := repos.New(paths.RepositoriesFile()).Lookup("o/pinned"); !ok {
		t.Error("the refusal must leave the store alone")
	}

	out, err = run(t, "repos", "forget", "o/absent", "--settings", settings)
	if err != nil || !strings.Contains(out, "No learned mapping") {
		t.Errorf("forgetting an unknown repository: %v\n%s", err, out)
	}
	if _, err := run(t, "repos", "forget", "not a repository", "--settings", settings); err == nil {
		t.Error("an unparsable repository must be an error")
	}
}

func TestReposPin(t *testing.T) {
	settings, home := reposFixture(t)
	out, err := run(t, "repos", "pin", "https://github.com/o/found.git", "--settings", settings)
	if err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	for _, want := range []string{
		`services.wrekin.settings.workspace.repositories."o/found" = "~/projects/found";`,
		`"repositories": {`,
		`"o/found": "~/projects/found"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, home) {
		t.Errorf("the home directory must be shortened to ~:\n%s", out)
	}
	data, _ := os.ReadFile(settings)
	if strings.Contains(string(data), "found") {
		t.Error("pin must not write the settings")
	}

	out, err = run(t, "repos", "pin", "o/pinned", "--settings", settings)
	if err != nil || !strings.Contains(out, "already") {
		t.Errorf("pinning a declared repository: %v\n%s", err, out)
	}
	if _, err := run(t, "repos", "pin", "o/absent", "--settings", settings); err == nil {
		t.Error("pinning a repository with no learned mapping must be an error")
	}
}

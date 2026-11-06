package repos

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/workspace"
)

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestVerify(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not installed")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	policy, err := workspace.NewPolicy([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "n")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "init", "-q")
	gitCmd(t, repo, "remote", "add", "origin", "git@github.com:o/n.git")
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	git := gitx.Runner{}

	if got, err := Verify(policy, git, repo, "o/n"); err != nil || got != repo {
		t.Errorf("valid checkout: %q, %v", got, err)
	}
	if got, err := Verify(policy, git, filepath.Join(repo, "sub"), "o/n"); err != nil || got != repo {
		t.Errorf("a subdirectory resolves to the top level: %q, %v", got, err)
	}
	if got, err := Verify(nil, git, repo, "o/n"); err != nil || got != repo {
		t.Errorf("without a policy: %q, %v", got, err)
	}
	cases := []struct {
		name, dir, repo string
		kind            Problem
	}{
		{"outside", outside, "o/n", ProblemNotAllowed},
		{"through a link", filepath.Join(root, "link"), "o/n", ProblemEscapesRoot},
		{"missing", filepath.Join(root, "absent"), "o/n", ProblemMissing},
		{"not a repository", plain, "o/n", ProblemNotRepository},
		{"other origin", repo, "o/other", ProblemOriginMismatch},
	}
	for _, c := range cases {
		_, err := Verify(policy, git, c.dir, c.repo)
		var ve *VerifyError
		if !errors.As(err, &ve) || ve.Problem != c.kind {
			t.Errorf("%s: err = %v, want %s", c.name, err, c.kind)
		}
	}
}

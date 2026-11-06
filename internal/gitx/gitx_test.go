package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func requireGit(t *testing.T) Runner {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not installed")
	}
	return Runner{}
}

// git runs a git command for test setup and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo creates a repository with one commit and the given origin.
func initRepo(t *testing.T, dir, origin string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-q", "-m", "initial")
	if origin != "" {
		git(t, dir, "remote", "add", "origin", origin)
	}
}

func TestRunnerEnvironmentDisablesPrompts(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /k")
	env := strings.Join(Runner{}.environment(), "\n")
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false", "GIT_SSH_COMMAND=ssh -i /k -o BatchMode=yes"} {
		if !strings.Contains(env, want) {
			t.Errorf("environment lacks %q:\n%s", want, env)
		}
	}
	t.Setenv("GIT_SSH_COMMAND", "")
	if !strings.Contains(strings.Join(Runner{}.environment(), "\n"), "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Error("default GIT_SSH_COMMAND must be ssh -o BatchMode=yes")
	}
}

func TestIsRepoTopLevelRemotes(t *testing.T) {
	r := requireGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo, "git@github.com:o/r.git")
	sub := filepath.Join(repo, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if !r.IsRepo(sub) {
		t.Error("a subdirectory of a repository is inside a repository")
	}
	if r.IsRepo(root) {
		t.Error("the plain root is not a repository")
	}
	top, err := r.TopLevel(sub)
	if err != nil {
		t.Fatal(err)
	}
	canon, _ := filepath.EvalSymlinks(repo)
	if top != canon {
		t.Errorf("TopLevel = %q, want %q", top, canon)
	}
	remotes, err := r.Remotes(repo)
	if err != nil {
		t.Fatal(err)
	}
	if remotes["origin"] != "git@github.com:o/r.git" {
		t.Errorf("remotes = %v", remotes)
	}
}

func TestFindCheckoutsMatchesRemoteForms(t *testing.T) {
	r := requireGit(t)
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "a"), "git@github.com:o/r.git")
	initRepo(t, filepath.Join(root, "work", "b"), "https://github.com/o/r")
	initRepo(t, filepath.Join(root, "c"), "https://github.com/o/other")
	initRepo(t, filepath.Join(root, ".cache", "d"), "https://github.com/o/r")
	initRepo(t, filepath.Join(root, "node_modules", "e"), "https://github.com/o/r")
	initRepo(t, filepath.Join(root, "deep", "x", "y", "f"), "https://github.com/o/r")
	initRepo(t, filepath.Join(root, "g"), "")

	got, err := r.FindCheckouts([]string{root}, "https://github.com/o/r.git", 3)
	if err != nil {
		t.Fatal(err)
	}
	canon := func(p string) string {
		c, _ := filepath.EvalSymlinks(p)
		return c
	}
	want := []string{canon(filepath.Join(root, "a")), canon(filepath.Join(root, "work", "b"))}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("FindCheckouts = %v, want %v", got, want)
	}
	deeper, err := r.FindCheckouts([]string{root}, "https://github.com/o/r.git", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(deeper) != 3 {
		t.Errorf("with depth 4 the deep checkout must be found too: %v", deeper)
	}
}

func TestHasUncommittedWorkSeesUntracked(t *testing.T) {
	r := requireGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	initRepo(t, repo, "")
	dirty, err := r.HasUncommittedWork(repo)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("a fresh repository is clean")
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = r.HasUncommittedWork(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("an untracked file is uncommitted work")
	}
}

func TestCloneLocalPath(t *testing.T) {
	r := requireGit(t)
	base := t.TempDir()
	src := filepath.Join(base, "src")
	initRepo(t, src, "")
	bare := filepath.Join(base, "bare.git")
	git(t, base, "clone", "-q", "--bare", src, bare)

	dest := filepath.Join(base, "clone")
	if err := r.Clone(context.Background(), "file://"+bare, dest); err == nil {
		t.Fatal("Clone from file:// must be refused unless AllowLocal is set")
	}
	r.AllowLocal = true
	if err := r.Clone(context.Background(), "file://"+bare, dest); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("clone has no README: %v", err)
	}
	if !r.IsRepo(dest) {
		t.Error("clone is not a repository")
	}
	remotes, _ := r.Remotes(dest)
	if remotes["origin"] != "file://"+bare {
		t.Errorf("origin = %q", remotes["origin"])
	}
	if err := r.Clone(context.Background(), filepath.Join(base, "missing.git"), filepath.Join(base, "clone2")); err == nil {
		t.Error("cloning a missing path must fail")
	} else if errors.Is(err, ErrPromptBlocked) {
		t.Errorf("a missing local path is not a credential prompt: %v", err)
	}
}

func TestCloneWithoutCredentialsIsPromptBlocked(t *testing.T) {
	r := requireGit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	err := r.Clone(ctx, "https://github.com/plmercereau/this-repo-does-not-exist-wrekin", filepath.Join(t.TempDir(), "dest"))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("cloning a non-existent private-looking repository must fail")
	}
	if elapsed >= 60*time.Second || ctx.Err() != nil {
		t.Fatalf("Clone did not return on its own: %v after %s", err, elapsed)
	}
	t.Logf("clone failed in %s: %v (prompt blocked: %v)", elapsed.Round(time.Millisecond), err, errors.Is(err, ErrPromptBlocked))
}

func TestClassifyPromptErrors(t *testing.T) {
	for _, s := range []string{
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"git@github.com: Permission denied (publickey).",
		"fatal: Authentication failed for 'https://x/'",
	} {
		if !looksLikePrompt(s) {
			t.Errorf("%q should be a blocked prompt", s)
		}
	}
	if looksLikePrompt("fatal: repository 'x' does not exist") {
		t.Error("a missing repository is not a prompt")
	}
}

func TestAddWorktreeAndExclude(t *testing.T) {
	r := requireGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	initRepo(t, repo, "")
	ctx := context.Background()
	wt := filepath.Join(repo, ".claude", "worktrees", "wrekin-abcd1234")

	if err := r.AddWorktree(ctx, repo, wt, "wrekin/abcd1234"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got := git(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "wrekin/abcd1234" {
		t.Errorf("worktree branch = %q", got)
	}
	if err := r.EnsureExcluded(repo, ".claude/worktrees/"); err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}
	if err := r.EnsureExcluded(repo, ".claude/worktrees/"); err != nil {
		t.Fatalf("EnsureExcluded again: %v", err)
	}
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(exclude), ".claude/worktrees/"); n != 1 {
		t.Errorf("pattern written %d times, want 1:\n%s", n, exclude)
	}
	if dirty, _ := r.HasUncommittedWork(repo); dirty {
		t.Error("the excluded worktree directory must not count as uncommitted work")
	}

	if err := r.RemoveWorktree(ctx, repo, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree directory still there: %v", err)
	}
	// The branch survives, so adding again reuses it instead of failing.
	if err := r.AddWorktree(ctx, repo, wt, "wrekin/abcd1234"); err != nil {
		t.Fatalf("AddWorktree with an existing branch: %v", err)
	}
	if got := git(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "wrekin/abcd1234" {
		t.Errorf("worktree branch after re-add = %q", got)
	}
}

// A checkout that lags behind origin (fennec's clone sat on a release while
// main moved on) must not seed the worktree: the new branch starts from
// origin's default branch after a fetch.
func TestAddWorktreeStartsFromAFreshOrigin(t *testing.T) {
	r := requireGit(t)
	r.AllowLocal = true
	base := t.TempDir()
	src := filepath.Join(base, "src")
	initRepo(t, src, "")
	bare := filepath.Join(base, "bare.git")
	git(t, base, "clone", "-q", "--bare", src, bare)
	checkout := filepath.Join(base, "checkout")
	git(t, base, "clone", "-q", "file://"+bare, checkout)

	// origin moves on; the checkout does not pull.
	if err := os.WriteFile(filepath.Join(src, "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "NEW.md")
	git(t, src, "commit", "-q", "-m", "second")
	git(t, src, "push", "-q", bare, "main")
	want := git(t, src, "rev-parse", "HEAD")

	wt := filepath.Join(checkout, ".claude", "worktrees", "wrekin-fresh")
	if err := r.AddWorktree(context.Background(), checkout, wt, "wrekin/fresh"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got := git(t, wt, "rev-parse", "HEAD"); got != want {
		t.Errorf("worktree at %s, want origin's %s", got, want)
	}
	if up, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "@{upstream}").Output(); err == nil {
		t.Errorf("the session branch must not track origin's default branch, tracks %s", up)
	}
}

// Without a reachable origin the worktree still starts, from HEAD.
func TestAddWorktreeFallsBackToHEADWhenOriginIsUnreachable(t *testing.T) {
	r := requireGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	initRepo(t, repo, "https://example.invalid/none.git")
	wt := filepath.Join(repo, ".claude", "worktrees", "wrekin-offline")
	if err := r.AddWorktree(context.Background(), repo, wt, "wrekin/offline"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got, want := git(t, wt, "rev-parse", "HEAD"), git(t, repo, "rev-parse", "HEAD"); got != want {
		t.Errorf("worktree at %s, want HEAD %s", got, want)
	}
}

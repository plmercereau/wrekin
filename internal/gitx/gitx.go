// Package gitx runs the git commands Wrekin needs. Every invocation is
// non-interactive: credential prompts are disabled and ssh runs in batch mode,
// so a missing credential fails fast instead of blocking the daemon.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/plmercereau/wrekin/internal/workspace"
)

// ErrPromptBlocked means git needed credentials it could only ask for
// interactively; Wrekin refuses to prompt.
var ErrPromptBlocked = errors.New("git asked for credentials interactively; refused")

// CloneTimeout bounds a clone.
const CloneTimeout = 10 * time.Minute

// commandTimeout bounds every other git command.
const commandTimeout = 60 * time.Second

// Runner executes git. The zero value uses the "git" found in PATH.
type Runner struct {
	Binary string
}

func (r Runner) binary() string {
	if r.Binary != "" {
		return r.Binary
	}
	return "git"
}

// environment is the process environment with prompts disabled.
func (r Runner) environment() []string {
	env := make([]string, 0, len(os.Environ())+5)
	sshCommand := "ssh"
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		switch key {
		case "GIT_TERMINAL_PROMPT", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_SSH_COMMAND", "GCM_INTERACTIVE":
			if key == "GIT_SSH_COMMAND" && value != "" {
				sshCommand = value
			}
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"SSH_ASKPASS=/bin/false",
		"GCM_INTERACTIVE=never",
		"GIT_SSH_COMMAND="+sshCommand+" -o BatchMode=yes",
	)
}

// run executes git in dir and returns stdout; the error carries stderr.
func (r Runner) run(ctx context.Context, dir string, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, r.binary(), args...)
	cmd.Dir = dir
	cmd.Env = r.environment()
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		return "", &CommandError{Args: args, Err: err, Stderr: strings.TrimSpace(stderr.String())}
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// CommandError is a failed git command with its stderr (kept local).
type CommandError struct {
	Args   []string
	Err    error
	Stderr string
}

func (e *CommandError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git %s: %v: %s", e.Args[0], e.Err, lastLine(e.Stderr))
	}
	return fmt.Sprintf("git %s: %v", e.Args[0], e.Err)
}

func (e *CommandError) Unwrap() error { return e.Err }

// lastLine returns the last non-empty line, where git puts its fatal message.
func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// IsRepo reports whether dir is inside a git work tree.
func (r Runner) IsRepo(dir string) bool {
	out, err := r.run(nil, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// TopLevel returns the canonical top-level directory of the work tree
// containing dir.
func (r Runner) TopLevel(dir string) (string, error) {
	out, err := r.run(nil, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(out)
}

// Remotes returns the fetch URL of each remote.
func (r Runner) Remotes(dir string) (map[string]string, error) {
	out, err := r.run(nil, dir, "remote", "-v")
	if err != nil {
		return nil, err
	}
	remotes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == "(fetch)" {
			remotes[fields[0]] = fields[1]
		}
	}
	return remotes, nil
}

// FindCheckouts walks each root down to maxDepth directories, skipping hidden
// directories and node_modules, and returns the canonical top levels whose
// origin names repoURL.
func (r Runner) FindCheckouts(roots []string, repoURL string, maxDepth int) ([]string, error) {
	if maxDepth <= 0 {
		maxDepth = 3
	}
	var found []string
	seen := map[string]bool{}
	for _, root := range roots {
		root = filepath.Clean(root)
		rootDepth := strings.Count(root, string(filepath.Separator))
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == root {
					return err
				}
				return nil // unreadable entries are skipped silently
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			depth := strings.Count(p, string(filepath.Separator)) - rootDepth
			if _, statErr := os.Lstat(filepath.Join(p, ".git")); statErr == nil {
				if remotes, rerr := r.Remotes(p); rerr == nil && workspace.SameRepository(remotes["origin"], repoURL) {
					canon, cerr := filepath.EvalSymlinks(p)
					if cerr != nil {
						canon = p
					}
					if !seen[canon] {
						seen[canon] = true
						found = append(found, canon)
					}
				}
				return filepath.SkipDir // do not look for repositories inside a repository
			}
			if depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	sort.Strings(found)
	return found, nil
}

// HasUncommittedWork reports whether the work tree has changes, untracked
// files included.
func (r Runner) HasUncommittedWork(dir string) (bool, error) {
	out, err := r.run(nil, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Clone clones url into dest without ever prompting. A credential prompt is
// reported as ErrPromptBlocked; the stderr stays in the returned error for
// the local log only.
func (r Runner) Clone(ctx context.Context, url, dest string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, CloneTimeout)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_, err := r.run(ctx, filepath.Dir(dest), "clone", "--", url, dest)
	if err == nil {
		return nil
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) && looksLikePrompt(cmdErr.Stderr) {
		return fmt.Errorf("%w: %s", ErrPromptBlocked, lastLine(cmdErr.Stderr))
	}
	return err
}

// looksLikePrompt recognises git failing because it could not ask for
// credentials.
func looksLikePrompt(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, marker := range []string{
		"terminal prompts disabled",
		"permission denied (publickey",
		"could not read username",
		"could not read password",
		"authentication failed",
		"host key verification failed",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// AddWorktree creates a worktree at path on branch, creating the branch from
// HEAD when it does not exist yet.
func (r Runner) AddWorktree(ctx context.Context, repo, path, branch string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := r.run(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = r.run(ctx, repo, "worktree", "add", "--", path, branch)
		return err
	}
	_, err := r.run(ctx, repo, "worktree", "add", "-b", branch, "--", path)
	return err
}

// RemoveWorktree removes a worktree Wrekin created and prunes the metadata.
// The branch is kept.
func (r Runner) RemoveWorktree(ctx context.Context, repo, path string) error {
	if _, err := r.run(ctx, repo, "worktree", "remove", "--force", "--", path); err != nil {
		if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
			return err
		}
	}
	_, err := r.run(ctx, repo, "worktree", "prune")
	return err
}

// EnsureExcluded appends pattern to the repository's info/exclude when it is
// not already listed.
func (r Runner) EnsureExcluded(repo, pattern string) error {
	excludePath, err := r.run(nil, repo, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(repo, excludePath)
	}
	existing, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + pattern + "\n")
	return err
}

package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/harness"
	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/repos"
	"github.com/plmercereau/wrekin/internal/workspace"
)

// fakeTerminal keeps sessions in memory. Tests drive the pane content and
// whether the launched program is still running.
type fakeTerminal struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
	launches []fakeLaunch
	// launchGate, when set, blocks Launch until it is closed.
	launchGate chan struct{}
	available  error
}

type fakeSession struct {
	running bool
	pane    string
}

type fakeLaunch struct {
	session, workdir string
	argv             []string
}

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{sessions: map[string]*fakeSession{}}
}

func (f *fakeTerminal) Name() string     { return "tmux" }
func (f *fakeTerminal) Available() error { return f.available }

func (f *fakeTerminal) Launch(ctx context.Context, session, workdir string, argv []string) error {
	f.mu.Lock()
	gate := f.launchGate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[session]; ok {
		return errors.New("duplicate session")
	}
	f.sessions[session] = &fakeSession{running: true, pane: "starting..."}
	f.launches = append(f.launches, fakeLaunch{session, workdir, argv})
	return nil
}

func (f *fakeTerminal) Exists(session string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sessions[session]
	return ok, nil
}

func (f *fakeTerminal) Running(session string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[session]
	return ok && s.running, nil
}

func (f *fakeTerminal) Capture(session string, lines int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[session]
	if !ok {
		return "", errors.New("no session")
	}
	return s.pane, nil
}

func (f *fakeTerminal) Kill(session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, session)
	return nil
}

func (f *fakeTerminal) List(prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for name := range f.sessions {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out, nil
}

func (f *fakeTerminal) AttachCommand(session string) []string {
	return []string{"tmux", "attach-session", "-t", "=" + session}
}

func (f *fakeTerminal) setPane(session, pane string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[session]; ok {
		s.pane = pane
	}
}

func (f *fakeTerminal) endSession(session string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, session)
}

func (f *fakeTerminal) launchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.launches)
}

// fakeHarness trusts the directories in its map and is "ready" when the pane
// shows READY.
type fakeHarness struct {
	mu       sync.Mutex
	trusted  map[string]bool
	trustErr error
	info     harness.Info
	// signedOut makes SignedIn answer false; signInErr makes it fail.
	signedOut bool
	signInErr error
	trusts    []string
}

func newFakeHarness() *fakeHarness {
	return &fakeHarness{trusted: map[string]bool{}, info: harness.Info{Available: true, Version: "0.0.1"}}
}

func (f *fakeHarness) ID() string                    { return "claude-code" }
func (f *fakeHarness) Detect() (harness.Info, error) { return f.info, nil }
func (f *fakeHarness) SignedIn(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.signedOut, f.signInErr
}
func (f *fakeHarness) Trusted(dir string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trusted[dir], nil
}
func (f *fakeHarness) Trust(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.trustErr != nil {
		return f.trustErr
	}
	f.trusted[dir] = true
	f.trusts = append(f.trusts, dir)
	return nil
}
func (f *fakeHarness) LaunchArgs(id string, opts harness.LaunchOptions) []string {
	if opts.RemoteControl != "" {
		if strings.HasPrefix(opts.RemoteControl, "-") || strings.Contains(opts.RemoteControl, "!") {
			return nil
		}
		return []string{"claude", "--teleport", id, "--remote-control", opts.RemoteControl}
	}
	return []string{"claude", "--teleport", id}
}
func (f *fakeHarness) RemoteControlActive(pane string) bool { return strings.Contains(pane, "RC-ON") }
func (f *fakeHarness) ReadyPattern() *regexp.Regexp         { return regexp.MustCompile(`READY`) }
func (f *fakeHarness) SetupScreen(pane string) string {
	if strings.Contains(pane, "SETUP") {
		return "its fake setup"
	}
	return ""
}
func (f *fakeHarness) ExitedIsFailure() bool { return true }

func (f *fakeHarness) setTrusted(dir string, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trusted[dir] = v
}

// env bundles a runner with its fakes over a temp root.
type env struct {
	t       *testing.T
	root    string
	store   *Store
	term    *fakeTerminal
	harn    *fakeHarness
	policy  *workspace.Policy
	learned *repos.Store
	runner  *Runner
	deps    RunnerDeps
}

func newEnv(t *testing.T, tweak func(*RunnerDeps)) *env {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	canon, _ := filepath.EvalSymlinks(root)
	policy, err := workspace.NewPolicy([]string{canon}, canon)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	learned := repos.New(filepath.Join(t.TempDir(), "repositories.json"))
	e := &env{t: t, root: canon, store: store, term: newFakeTerminal(), harn: newFakeHarness(), policy: policy, learned: learned}
	e.deps = RunnerDeps{
		Store:   store,
		Policy:  policy,
		Learned: learned,
		// The tests clone from local bare repositories; production refuses file://.
		Git:           gitx.Runner{AllowLocal: true},
		Term:          e.term,
		Harness:       e.harn,
		MaxConcurrent: 4,
		ReadyTimeout:  5 * time.Second,
		PollInterval:  5 * time.Millisecond,
	}
	if tweak != nil {
		tweak(&e.deps)
	}
	e.runner = NewRunner(e.deps)
	t.Cleanup(e.runner.Close)
	return e
}

// waitState polls until the job reaches state (or fails the test).
func (e *env) waitState(id string, state protocol.State) *Job {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last *Job
	for time.Now().Before(deadline) {
		j, jerr := e.runner.Status(id)
		if jerr != nil {
			e.t.Fatalf("Status(%s): %v", id, jerr)
		}
		last = j
		if j.State == state {
			return j
		}
		if j.State == protocol.StateFailed && state != protocol.StateFailed {
			e.t.Fatalf("job %s failed while waiting for %s: %+v", id, state, j.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("job %s did not reach %s; last state %s (%+v)", id, state, last.State, last.Error)
	return nil
}

// waitLaunched waits until the n-th launch happened and returns it.
func (e *env) waitLaunched(n int) fakeLaunch {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e.term.mu.Lock()
		if len(e.term.launches) >= n {
			l := e.term.launches[n-1]
			e.term.mu.Unlock()
			return l
		}
		e.term.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("launch %d did not happen", n)
	return fakeLaunch{}
}

// git runs a git command for test setup.
func gitCmd(t *testing.T, dir string, args ...string) string {
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

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not installed")
	}
}

// initRepo creates a repository with one commit under dir.
func initRepo(t *testing.T, dir, origin string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "README.md")
	gitCmd(t, dir, "commit", "-q", "-m", "initial")
	if origin != "" {
		gitCmd(t, dir, "remote", "add", "origin", origin)
	}
}

// bareRepo creates a bare repository and returns its file:// URL.
func bareRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "src")
	initRepo(t, src, "")
	bare := filepath.Join(base, "upstream.git")
	gitCmd(t, base, "clone", "-q", "--bare", src, bare)
	return "file://" + bare
}

func sessionID(n int) string { return fmt.Sprintf("session_%024d", n) }

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/jobs"
	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/workspace"
)

type fakeRunner struct {
	teleports []protocol.TeleportParams
	statuses  []string
	approvals []protocol.ApproveParams
	fail      *protocol.JobError
}

func (f *fakeRunner) job(id string) *jobs.Job {
	return &jobs.Job{Job: protocol.Job{ID: id, Harness: "claude-code", CloudSessionID: "s", State: protocol.StateAccepted}, CreatedClone: true}
}

func (f *fakeRunner) Teleport(ctx context.Context, p protocol.TeleportParams) (*jobs.Job, *protocol.JobError) {
	f.teleports = append(f.teleports, p)
	if f.fail != nil {
		return nil, f.fail
	}
	return f.job("job_t"), nil
}

func (f *fakeRunner) Status(id string) (*jobs.Job, *protocol.JobError) {
	f.statuses = append(f.statuses, id)
	if id == "job_missing" {
		return nil, &protocol.JobError{Code: protocol.CodeUnknownJob, Message: "no job of that id"}
	}
	return f.job(id), nil
}

func (f *fakeRunner) Approve(id string, approved bool) (*jobs.Job, *protocol.JobError) {
	f.approvals = append(f.approvals, protocol.ApproveParams{JobID: id, Approved: approved})
	return f.job(id), nil
}

func (f *fakeRunner) List() ([]*jobs.Job, *protocol.JobError) {
	return []*jobs.Job{f.job("job_1"), f.job("job_2")}, nil
}

func command(t *testing.T, id, name string, params any) protocol.Command {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Command{Envelope: protocol.Envelope{V: 1, Type: "command"}, ID: id, Name: name, Params: raw}
}

func roundTrip(t *testing.T, res protocol.Result) map[string]any {
	t.Helper()
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDaemonHandlerMapsCommands(t *testing.T) {
	fr := &fakeRunner{}
	h := NewHandler(fr, func() time.Time { return time.Date(2026, 11, 6, 1, 2, 3, 0, time.UTC) })
	ctx := context.Background()

	res := h.Handle(ctx, command(t, "c1", "teleport", protocol.TeleportParams{Harness: "claude-code", CloudSessionID: "session_1", Repository: "o/r"}))
	m := roundTrip(t, res)
	if m["v"] != float64(1) || m["type"] != "result" || m["id"] != "c1" || m["ok"] != true {
		t.Errorf("teleport result header: %v", m)
	}
	if len(fr.teleports) != 1 || fr.teleports[0].Repository != "o/r" {
		t.Errorf("teleport params not passed: %+v", fr.teleports)
	}
	data := m["data"].(map[string]any)
	if data["id"] != "job_t" || data["state"] != "accepted" {
		t.Errorf("teleport data = %v", data)
	}
	if _, leaked := data["createdClone"]; leaked {
		t.Error("local-only fields must not reach the relay")
	}

	res = h.Handle(ctx, command(t, "c2", "get_teleport_status", protocol.StatusParams{JobID: "job_9"}))
	if !res.OK || fr.statuses[0] != "job_9" {
		t.Errorf("status result = %+v statuses=%v", res, fr.statuses)
	}
	res = h.Handle(ctx, command(t, "c3", "get_teleport_status", protocol.StatusParams{JobID: "job_missing"}))
	if res.OK || res.Error == nil || res.Error.Code != protocol.CodeUnknownJob {
		t.Errorf("unknown job result = %+v", res)
	}
	res = h.Handle(ctx, command(t, "c4", "get_teleport_status", map[string]any{}))
	if res.OK || res.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("missing jobId result = %+v", res)
	}

	res = h.Handle(ctx, command(t, "c5", "approve_repository", protocol.ApproveParams{JobID: "job_a", Approved: true}))
	if !res.OK || len(fr.approvals) != 1 || !fr.approvals[0].Approved || fr.approvals[0].JobID != "job_a" {
		t.Errorf("approve result = %+v approvals=%v", res, fr.approvals)
	}

	res = h.Handle(ctx, command(t, "c6", "list_jobs", map[string]any{}))
	m = roundTrip(t, res)
	list := m["data"].(map[string]any)["jobs"].([]any)
	if len(list) != 2 {
		t.Errorf("list_jobs = %v", m["data"])
	}

	res = h.Handle(ctx, command(t, "c7", "ping", map[string]any{}))
	m = roundTrip(t, res)
	if m["data"].(map[string]any)["at"] != "2026-11-06T01:02:03Z" {
		t.Errorf("ping = %v", m["data"])
	}

	res = h.Handle(ctx, command(t, "c8", "format_disk", map[string]any{}))
	if res.OK || res.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("unknown command result = %+v", res)
	}
	res = h.Handle(ctx, protocol.Command{ID: "c9", Name: "teleport", Params: json.RawMessage(`{"harness": 42}`)})
	if res.OK || res.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("malformed params result = %+v", res)
	}

	fr.fail = &protocol.JobError{Code: protocol.CodeConcurrencyLimit, Message: "busy"}
	res = h.Handle(ctx, command(t, "c10", "teleport", protocol.TeleportParams{Harness: "claude-code", CloudSessionID: "s"}))
	if res.OK || res.Error.Code != protocol.CodeConcurrencyLimit || res.Error.Message != "busy" {
		t.Errorf("runner error result = %+v", res)
	}
}

func TestRotatingLogWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrekin.log")
	w, err := newRotatingWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n"
	for i := 0; i < 10; i++ { // 400 bytes: enough for several rotations
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, want := range []string{"wrekin.log", "wrekin.log.1", "wrekin.log.2", "wrekin.log.3"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), want)); err != nil {
			t.Errorf("%s missing (have %v)", want, names)
		}
	}
	if _, err := os.Stat(path + ".4"); err == nil {
		t.Error("more than 3 rotated files kept")
	}
	info, _ := os.Stat(path)
	if info.Size() > 100 {
		t.Errorf("active log is %d bytes, over the limit", info.Size())
	}
}

func TestRunWithoutIdentityExplains(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Run(ctx, Options{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Version: "test"})
	if err == nil || !strings.Contains(err.Error(), "wrekin auth") {
		t.Fatalf("err = %v, want a hint to run wrekin auth", err)
	}
}

// fakeTerm is a terminal without sessions; cleanup only asks it about the
// sessions a job records.
type fakeTerm struct{}

func (fakeTerm) Name() string                                           { return "tmux" }
func (fakeTerm) Available() error                                       { return nil }
func (fakeTerm) Launch(context.Context, string, string, []string) error { return nil }
func (fakeTerm) Exists(string) (bool, error)                            { return false, nil }
func (fakeTerm) Running(string) (bool, error)                           { return false, nil }
func (fakeTerm) Capture(string, int) (string, error)                    { return "", nil }
func (fakeTerm) Kill(string) error                                      { return nil }
func (fakeTerm) List(string) ([]string, error)                          { return nil, nil }
func (fakeTerm) AttachCommand(string) []string                          { return nil }

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// cleanup re-checks every path a job records before removing anything:
// jobs.json is written only by the daemon, but a bug there must not turn into
// the removal of a user directory or of an allowed root. The refused jobs are
// reported under Errors and their directories are left alone; the worktrees
// are real so that git would remove them if asked.
func TestCleanupRefusesPathsOutsidePolicy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy, err := workspace.NewPolicy([]string{root}, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	checkout := filepath.Join(root, "repo")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, checkout, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, checkout, "add", "README.md")
	gitCmd(t, checkout, "commit", "-q", "-m", "initial")
	elsewhere := filepath.Join(outside, "elsewhere")
	gitCmd(t, checkout, "worktree", "add", "-q", "-b", "wt-outside", "--", elsewhere)
	inside := filepath.Join(checkout, "src")
	gitCmd(t, checkout, "worktree", "add", "-q", "-b", "wt-inside", "--", inside)
	good := filepath.Join(checkout, ".claude", "worktrees", "good")
	gitCmd(t, checkout, "worktree", "add", "-q", "-b", "wt-good", "--", good)
	clone := filepath.Join(root, "clone")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}

	worktreeJob := func(id, workdir, checkout string) {
		j := &jobs.Job{Job: protocol.Job{ID: id, State: protocol.StateFailed, Workdir: workdir}, CreatedWorktree: true, CheckoutDir: checkout}
		if err := store.Put(j); err != nil {
			t.Fatal(err)
		}
	}
	cloneJob := func(id, checkout string) {
		j := &jobs.Job{Job: protocol.Job{ID: id, State: protocol.StateFailed}, CreatedClone: true, CheckoutDir: checkout}
		if err := store.Put(j); err != nil {
			t.Fatal(err)
		}
	}
	worktreeJob("job_wt_outside", elsewhere, checkout)
	worktreeJob("job_wt_not_under_worktrees", inside, checkout)
	worktreeJob("job_wt_checkout_outside", filepath.Join(outside, ".claude", "worktrees", "x"), outside)
	cloneJob("job_clone_is_root", root)
	worktreeJob("job_wt_good", good, checkout)
	cloneJob("job_clone_good", clone)

	d := &daemon{store: store, term: fakeTerm{}, git: gitx.Runner{}, policy: policy, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	report := d.cleanup(context.Background(), false)

	for _, dir := range []string{elsewhere, inside, root, checkout} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s must still exist: %v", dir, err)
		}
	}
	for _, dir := range []string{good, clone} {
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s should have been removed (stat: %v)", dir, err)
		}
	}
	if len(report.Errors) != 4 {
		t.Errorf("errors = %v, want one per refused job", report.Errors)
	}
	for _, want := range []string{elsewhere, inside, root, outside} {
		found := false
		for _, e := range report.Errors {
			if strings.Contains(e, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("errors %v do not name %s", report.Errors, want)
		}
	}
	if len(report.Worktrees) != 1 || report.Worktrees[0] != good {
		t.Errorf("worktrees = %v, want only %s", report.Worktrees, good)
	}
	if len(report.Clones) != 1 || report.Clones[0] != clone {
		t.Errorf("clones = %v, want only %s", report.Clones, clone)
	}
	// The job records are dropped either way; the refusal is in the report.
	if len(report.Jobs) != 6 || len(store.List()) != 0 {
		t.Errorf("jobs = %v, remaining %d", report.Jobs, len(store.List()))
	}
}

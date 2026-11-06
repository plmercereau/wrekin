package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/protocol"
)

func teleportParams(n int, repo string) protocol.TeleportParams {
	return protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(n), Repository: repo}
}

func TestTeleportRequiresRepositoryOrDirectory(t *testing.T) {
	e := newEnv(t, nil)
	_, jerr := e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(1)})
	if jerr == nil || jerr.Code != protocol.CodeRepositoryRequired {
		t.Fatalf("err = %v, want repository_required", jerr)
	}
	_, jerr = e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", Repository: "o/r"})
	if jerr == nil || jerr.Code != protocol.CodeInvalidParams {
		t.Fatalf("missing session id: err = %v, want invalid_params", jerr)
	}
	_, jerr = e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "other", CloudSessionID: sessionID(1), Repository: "o/r"})
	if jerr == nil || jerr.Code != protocol.CodeInvalidParams {
		t.Fatalf("unknown harness: err = %v, want invalid_params", jerr)
	}
	_, jerr = e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: "bad id; rm -rf", Repository: "o/r"})
	if jerr == nil || jerr.Code != protocol.CodeInvalidParams {
		t.Fatalf("hostile session id: err = %v, want invalid_params", jerr)
	}
	// A flag-shaped id would be read by the harness as another option.
	for _, flag := range []string{"--dangerously-skip-permissions", "-r", "--continue", "abcdef0123"} {
		_, jerr = e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: flag, Repository: "o/r"})
		if jerr == nil || jerr.Code != protocol.CodeInvalidParams {
			t.Fatalf("session id %q: err = %v, want invalid_params", flag, jerr)
		}
	}
	if len(e.store.List()) != 0 {
		t.Error("no job must be created for invalid requests")
	}
}

func TestTeleportFindsExistingCheckoutAndBecomesReady(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	checkout := filepath.Join(e.root, "wrekin")
	initRepo(t, checkout, "git@github.com:plmercereau/wrekin.git")
	e.harn.setTrusted(checkout, true)

	job, jerr := e.runner.Teleport(context.Background(), teleportParams(1, "plmercereau/wrekin"))
	if jerr != nil {
		t.Fatalf("Teleport: %v", jerr)
	}
	if job.State != protocol.StateAccepted && job.State != protocol.StatePreparing {
		t.Errorf("initial state %s", job.State)
	}
	if !strings.HasPrefix(job.ID, "job_") {
		t.Errorf("job id %q", job.ID)
	}
	launched := e.waitLaunched(1)
	short := strings.ToLower(sessionID(1)[len(sessionID(1))-8:])
	wantWorkdir := filepath.Join(checkout, ".claude", "worktrees", "wrekin-"+short)
	if launched.workdir != wantWorkdir {
		t.Errorf("launched in %q, want %q", launched.workdir, wantWorkdir)
	}
	if launched.session != "wrekin-"+short {
		t.Errorf("session %q", launched.session)
	}
	if strings.Join(launched.argv, " ") != "claude --teleport "+sessionID(1) {
		t.Errorf("argv %v", launched.argv)
	}
	e.term.setPane(launched.session, "banner\nREADY\n")
	ready := e.waitState(job.ID, protocol.StateReady)
	if ready.Ready == nil || !ready.Ready.Confirmed {
		t.Errorf("ready = %+v", ready.Ready)
	}
	if ready.Session == nil || ready.Session.Name != launched.session || ready.Session.Attach != "wrekin attach "+launched.session || ready.Session.Terminal != "tmux" {
		t.Errorf("session = %+v", ready.Session)
	}
	if ready.Repository == nil || ready.Repository.URL != "https://github.com/plmercereau/wrekin" || ready.Repository.Name != "wrekin" || ready.Repository.Input != "plmercereau/wrekin" {
		t.Errorf("repository = %+v", ready.Repository)
	}
	if ready.Workdir != wantWorkdir || !ready.CreatedWorktree || ready.CreatedClone || ready.CheckoutDir != checkout {
		t.Errorf("local fields: workdir=%q worktree=%v clone=%v checkout=%q", ready.Workdir, ready.CreatedWorktree, ready.CreatedClone, ready.CheckoutDir)
	}
	if ok, _ := e.harn.Trusted(wantWorkdir); !ok {
		t.Error("the worktree must have been trusted because the checkout was")
	}
	if ready.UpdatedAt.Before(ready.CreatedAt) || ready.UpdatedAt.Location() != time.UTC {
		t.Errorf("timestamps: created %v updated %v", ready.CreatedAt, ready.UpdatedAt)
	}
}

func TestWorktreeIsolationCreatesBranchAndExclude(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	checkout := filepath.Join(e.root, "repo")
	initRepo(t, checkout, "https://github.com/o/r")
	e.harn.setTrusted(checkout, true)
	if _, jerr := e.runner.Teleport(context.Background(), teleportParams(7, "o/r")); jerr != nil {
		t.Fatal(jerr)
	}
	e.waitLaunched(1)
	short := strings.ToLower(sessionID(7)[len(sessionID(7))-8:])
	wt := filepath.Join(checkout, ".claude", "worktrees", "wrekin-"+short)
	if got := gitCmd(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "wrekin/"+short {
		t.Errorf("worktree branch %q, want wrekin/%s", got, short)
	}
	exclude, err := os.ReadFile(filepath.Join(checkout, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), ".claude/worktrees/") {
		t.Errorf("exclude not written: %v %q", err, exclude)
	}
	if out := gitCmd(t, checkout, "status", "--porcelain"); out != "" {
		t.Errorf("the checkout must stay clean, got %q", out)
	}
}

func TestTeleportUnknownRepositoryNeedsAttentionWithProposedPath(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(2, "plmercereau/absent"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	j := e.waitState(job.ID, protocol.StateNeedsAttention)
	if j.Attention == nil || j.Attention.Kind != protocol.AttentionApproveRepository {
		t.Fatalf("attention = %+v", j.Attention)
	}
	if j.Attention.ProposedPath != filepath.Join(e.root, "absent") {
		t.Errorf("proposedPath = %q", j.Attention.ProposedPath)
	}
	if j.Attention.RepositoryURL != "https://github.com/plmercereau/absent" || j.Attention.Message == "" {
		t.Errorf("attention = %+v", j.Attention)
	}
	// One approval both clones and trusts the directory for the harness; the
	// message must say so (spec section 5.7).
	if !strings.Contains(j.Attention.Message, "trusted") || !strings.Contains(j.Attention.Message, j.Attention.ProposedPath) {
		t.Errorf("the clone message must name the trust it grants and the path: %q", j.Attention.Message)
	}
	if e.term.launchCount() != 0 {
		t.Error("nothing may be launched before approval")
	}
}

func TestApproveClonesAndLaunches(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	url := bareRepo(t)
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(3, url))
	if jerr != nil {
		t.Fatal(jerr)
	}
	j := e.waitState(job.ID, protocol.StateNeedsAttention)
	proposed := j.Attention.ProposedPath
	if proposed != filepath.Join(e.root, "upstream") {
		t.Errorf("proposedPath = %q", proposed)
	}

	approved, jerr := e.runner.Approve(job.ID, true)
	if jerr != nil {
		t.Fatalf("Approve: %v", jerr)
	}
	if approved.State != protocol.StatePreparing {
		t.Errorf("state after approval %s, want preparing", approved.State)
	}
	if approved.Attention != nil {
		t.Error("attention must be cleared after the decision")
	}
	launched := e.waitLaunched(1)
	if _, err := os.Stat(filepath.Join(proposed, "README.md")); err != nil {
		t.Errorf("clone missing: %v", err)
	}
	short := strings.ToLower(sessionID(3)[len(sessionID(3))-8:])
	if launched.workdir != filepath.Join(proposed, ".claude", "worktrees", "wrekin-"+short) {
		t.Errorf("workdir %q", launched.workdir)
	}
	e.term.setPane(launched.session, "READY")
	done := e.waitState(job.ID, protocol.StateReady)
	if !done.CreatedClone || !done.CreatedWorktree || done.CheckoutDir != proposed || done.ApprovedAt == nil {
		t.Errorf("local fields after clone: %+v", done)
	}
	// The approval trusts both the checkout and the worktree.
	for _, dir := range []string{proposed, launched.workdir} {
		if ok, _ := e.harn.Trusted(dir); !ok {
			t.Errorf("%s should be trusted after approval", dir)
		}
	}
	if _, jerr := e.runner.Approve(job.ID, true); jerr == nil || jerr.Code != protocol.CodeAttentionNotPending {
		t.Errorf("second approval: %v, want attention_not_pending", jerr)
	}
}

func TestApproveDeclinedFails(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	job, _ := e.runner.Teleport(context.Background(), teleportParams(4, "o/missing"))
	e.waitState(job.ID, protocol.StateNeedsAttention)
	declined, jerr := e.runner.Approve(job.ID, false)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if declined.State != protocol.StateFailed || declined.Error == nil || declined.Error.Code != protocol.CodeNotApproved {
		t.Errorf("declined job = %s %+v", declined.State, declined.Error)
	}
	if _, jerr := e.runner.Approve(job.ID, false); jerr == nil || jerr.Code != protocol.CodeAttentionNotPending {
		t.Errorf("Approve on a failed job: %v", jerr)
	}
	if _, jerr := e.runner.Approve("job_nope", true); jerr == nil || jerr.Code != protocol.CodeUnknownJob {
		t.Errorf("Approve on an unknown job: %v", jerr)
	}
	if _, jerr := e.runner.Status("job_nope"); jerr == nil || jerr.Code != protocol.CodeUnknownJob {
		t.Errorf("Status on an unknown job: %v", jerr)
	}
	// A failed job is only replaced with relaunch.
	again, jerr := e.runner.Teleport(context.Background(), teleportParams(4, "o/missing"))
	if jerr != nil || again.ID != job.ID {
		t.Errorf("repeat without relaunch should return the failed job: %v %v", again, jerr)
	}
	p := teleportParams(4, "o/missing")
	p.Relaunch = true
	fresh, jerr := e.runner.Teleport(context.Background(), p)
	if jerr != nil || fresh.ID == job.ID {
		t.Errorf("relaunch should create a new job: %v %v", fresh, jerr)
	}
}

func TestRepeatTeleportReturnsSameJob(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	checkout := filepath.Join(e.root, "repo")
	initRepo(t, checkout, "https://github.com/o/r")
	e.harn.setTrusted(checkout, true)
	gate := make(chan struct{})
	e.term.launchGate = gate

	first, jerr := e.runner.Teleport(context.Background(), teleportParams(5, "o/r"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	e.waitState(first.ID, protocol.StateLaunching)
	second, jerr := e.runner.Teleport(context.Background(), teleportParams(5, "o/r"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if second.ID != first.ID {
		t.Errorf("second teleport created %s instead of returning %s", second.ID, first.ID)
	}
	close(gate)
	launched := e.waitLaunched(1)
	time.Sleep(20 * time.Millisecond)
	if e.term.launchCount() != 1 {
		t.Errorf("Launch called %d times, want 1", e.term.launchCount())
	}
	e.term.setPane(launched.session, "READY")
	e.waitState(first.ID, protocol.StateReady)
	third, jerr := e.runner.Teleport(context.Background(), teleportParams(5, "o/r"))
	if jerr != nil || third.ID != first.ID {
		t.Errorf("a ready job with a live session is returned as is: %v %v", third, jerr)
	}
	if e.term.launchCount() != 1 {
		t.Errorf("Launch called %d times, want 1", e.term.launchCount())
	}
}

func TestReadySessionGoneAnswersSessionGoneThenRelaunch(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	checkout := filepath.Join(e.root, "repo")
	initRepo(t, checkout, "https://github.com/o/r")
	e.harn.setTrusted(checkout, true)
	job, _ := e.runner.Teleport(context.Background(), teleportParams(6, "o/r"))
	first := e.waitLaunched(1)
	session := first.session
	e.term.setPane(session, "READY")
	e.waitState(job.ID, protocol.StateReady)

	e.term.endSession(session)
	_, jerr := e.runner.Teleport(context.Background(), teleportParams(6, "o/r"))
	if jerr == nil || jerr.Code != protocol.CodeSessionGone {
		t.Fatalf("err = %v, want session_gone", jerr)
	}
	st, _ := e.runner.Status(job.ID)
	if st.State != protocol.StateFailed || st.Error == nil || st.Error.Code != protocol.CodeSessionGone {
		t.Errorf("job after session gone = %s %+v", st.State, st.Error)
	}
	if e.term.launchCount() != 1 {
		t.Fatal("nothing may be launched without relaunch")
	}
	// Still refused without relaunch.
	if _, jerr := e.runner.Teleport(context.Background(), teleportParams(6, "o/r")); jerr == nil || jerr.Code != protocol.CodeSessionGone {
		t.Fatalf("second attempt: %v, want session_gone", jerr)
	}
	p := teleportParams(6, "o/r")
	p.Relaunch = true
	fresh, jerr := e.runner.Teleport(context.Background(), p)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if fresh.ID == job.ID {
		t.Error("relaunch must create a new job")
	}
	second := e.waitLaunched(2)
	if e.term.launchCount() != 2 {
		t.Errorf("Launch count %d, want 2", e.term.launchCount())
	}
	// The worktree from the first job is reused rather than failing.
	e.term.setPane(second.session, "READY")
	done := e.waitState(fresh.ID, protocol.StateReady)
	if done.Workdir != first.workdir || done.CreatedWorktree {
		t.Errorf("relaunch workdir %q differs from %q, or claims to have created the worktree (%v)", done.Workdir, first.workdir, done.CreatedWorktree)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	requireGit(t)
	e := newEnv(t, func(d *RunnerDeps) { d.MaxConcurrent = 1 })
	checkout := filepath.Join(e.root, "repo")
	initRepo(t, checkout, "https://github.com/o/r")
	e.harn.setTrusted(checkout, true)
	gate := make(chan struct{})
	e.term.launchGate = gate
	first, jerr := e.runner.Teleport(context.Background(), teleportParams(8, "o/r"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	e.waitState(first.ID, protocol.StateLaunching)
	_, jerr = e.runner.Teleport(context.Background(), teleportParams(9, "o/r"))
	if jerr == nil || jerr.Code != protocol.CodeConcurrencyLimit {
		t.Fatalf("err = %v, want concurrency_limit", jerr)
	}
	close(gate)
	e.term.setPane(e.waitLaunched(1).session, "READY")
	e.waitState(first.ID, protocol.StateReady)
	if _, jerr := e.runner.Teleport(context.Background(), teleportParams(9, "o/r")); jerr != nil {
		t.Errorf("after the first job is ready a new one is accepted: %v", jerr)
	}
}

func TestUntrustedDirectoryNeedsAttentionThenTrustOnApprove(t *testing.T) {
	e := newEnv(t, nil)
	dir := filepath.Join(e.root, "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	job, jerr := e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(10), Directory: dir})
	if jerr != nil {
		t.Fatal(jerr)
	}
	j := e.waitState(job.ID, protocol.StateNeedsAttention)
	if j.Attention.Kind != protocol.AttentionApproveRepository || j.Attention.ProposedPath != dir || !strings.Contains(j.Attention.Message, "trust") {
		t.Errorf("attention = %+v", j.Attention)
	}
	if j.Repository != nil {
		t.Errorf("a plain directory has no repository: %+v", j.Repository)
	}
	if _, jerr := e.runner.Approve(job.ID, true); jerr != nil {
		t.Fatal(jerr)
	}
	launched := e.waitLaunched(1)
	if ok, _ := e.harn.Trusted(dir); !ok {
		t.Error("approval must trust the directory")
	}
	if launched.workdir != dir || launched.session != "wrekin-"+strings.ToLower(sessionID(10)[len(sessionID(10))-8:]) {
		t.Errorf("launch = %+v", launched)
	}
	e.term.setPane(launched.session, "READY")
	done := e.waitState(job.ID, protocol.StateReady)
	if done.CreatedWorktree || done.CreatedClone || done.Workdir != dir {
		t.Errorf("a plain directory is used as is: %+v", done)
	}
}

func TestDirectoryPolicyErrors(t *testing.T) {
	e := newEnv(t, nil)
	outside := t.TempDir()
	job, jerr := e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(11), Directory: outside})
	if jerr != nil {
		t.Fatal(jerr)
	}
	failed := e.waitState(job.ID, protocol.StateFailed)
	if failed.Error == nil || failed.Error.Code != protocol.CodeDirectoryNotAllowed {
		t.Errorf("error = %+v, want directory_not_allowed", failed.Error)
	}
	if strings.Contains(failed.Error.Message, outside) {
		t.Errorf("the message must not leak a path outside the roots: %q", failed.Error.Message)
	}

	if err := os.Symlink(outside, filepath.Join(e.root, "link")); err != nil {
		t.Fatal(err)
	}
	job, jerr = e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(12), Directory: filepath.Join(e.root, "link", "x")})
	if jerr != nil {
		t.Fatal(jerr)
	}
	failed = e.waitState(job.ID, protocol.StateFailed)
	if failed.Error == nil || failed.Error.Code != protocol.CodePathEscapesRoot {
		t.Errorf("error = %+v, want path_escapes_root", failed.Error)
	}
	job, jerr = e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(13), Directory: filepath.Join(e.root, "does-not-exist")})
	if jerr != nil {
		t.Fatal(jerr)
	}
	failed = e.waitState(job.ID, protocol.StateFailed)
	if failed.Error == nil || failed.Error.Code != protocol.CodeDirectoryNotAllowed {
		t.Errorf("missing directory error = %+v", failed.Error)
	}
}

func TestHarnessExitBeforeReadyFails(t *testing.T) {
	e := newEnv(t, nil)
	dir := filepath.Join(e.root, "plain")
	_ = os.MkdirAll(dir, 0o755)
	e.harn.setTrusted(dir, true)
	job, _ := e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(14), Directory: dir})
	e.term.endSession(e.waitLaunched(1).session)
	failed := e.waitState(job.ID, protocol.StateFailed)
	if failed.Error == nil || failed.Error.Code != protocol.CodeHarnessExited {
		t.Errorf("error = %+v, want harness_exited", failed.Error)
	}
	if failed.Ready != nil {
		t.Error("a failed job has no ready block")
	}
}

func TestReadyTimeoutIsUnconfirmed(t *testing.T) {
	e := newEnv(t, func(d *RunnerDeps) { d.ReadyTimeout = 60 * time.Millisecond })
	dir := filepath.Join(e.root, "plain")
	_ = os.MkdirAll(dir, 0o755)
	e.harn.setTrusted(dir, true)
	job, _ := e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(15), Directory: dir})
	ready := e.waitState(job.ID, protocol.StateReady)
	if ready.Ready == nil || ready.Ready.Confirmed || !strings.Contains(ready.Ready.Detail, "not seen") {
		t.Errorf("ready = %+v", ready.Ready)
	}
}

func TestHarnessAndTerminalAvailability(t *testing.T) {
	e := newEnv(t, nil)
	e.harn.info.Available = false
	_, jerr := e.runner.Teleport(context.Background(), teleportParams(16, "o/r"))
	if jerr == nil || jerr.Code != protocol.CodeHarnessUnavailable {
		t.Errorf("err = %v, want harness_unavailable", jerr)
	}
	e.harn.info.Available = true
	e.term.available = context.DeadlineExceeded
	_, jerr = e.runner.Teleport(context.Background(), teleportParams(16, "o/r"))
	if jerr == nil || jerr.Code != protocol.CodeTerminalUnavailable {
		t.Errorf("err = %v, want terminal_unavailable", jerr)
	}
}

func TestRecoverMarksInterruptedJobsFailed(t *testing.T) {
	e := newEnv(t, nil)
	now := time.Now().UTC().Truncate(time.Second)
	for i, st := range []protocol.State{protocol.StatePreparing, protocol.StateLaunching, protocol.StateReady, protocol.StateNeedsAttention, protocol.StateAccepted} {
		j := &Job{Job: protocol.Job{ID: "job_" + string(rune('a'+i)), Harness: "claude-code", CloudSessionID: sessionID(20 + i), State: st, CreatedAt: now, UpdatedAt: now}}
		if st == protocol.StateReady {
			j.Session = &protocol.Session{Terminal: "tmux", Name: "gone"}
			j.Ready = &protocol.Ready{Confirmed: true}
		}
		if err := e.store.Put(j); err != nil {
			t.Fatal(err)
		}
	}
	e.runner.Recover()
	want := map[string]protocol.State{"job_a": protocol.StateFailed, "job_b": protocol.StateFailed, "job_c": protocol.StateReady, "job_d": protocol.StateNeedsAttention, "job_e": protocol.StateFailed}
	for id, st := range want {
		j, _ := e.store.Get(id)
		if j.State != st {
			t.Errorf("%s: state %s, want %s", id, j.State, st)
		}
		if st == protocol.StateFailed && (j.Error == nil || j.Error.Code != protocol.CodeInternal || !strings.Contains(j.Error.Message, "restarted")) {
			t.Errorf("%s: error %+v", id, j.Error)
		}
	}
	// The ready job whose session is gone stays ready until asked again.
	_, jerr := e.runner.Teleport(context.Background(), teleportParams(22, "o/r"))
	if jerr == nil || jerr.Code != protocol.CodeSessionGone {
		t.Errorf("err = %v, want session_gone", jerr)
	}
	list, _ := e.runner.List()
	if len(list) != 5 {
		t.Errorf("List returned %d jobs", len(list))
	}
}

// An allowed root inside a larger work tree (the home directory kept as a
// repository): the directory passes the policy, the repository's top level
// does not, and nothing outside the root may be used or trusted.
func TestCheckoutTopLevelOutsideRootsIsRefused(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	// e.root sits inside a fresh repository rooted one level above it.
	parent := filepath.Dir(e.root)
	initRepo(t, parent, "git@github.com:someone/home.git")
	sub := filepath.Join(e.root, "notes")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	job, jerr := e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(7), Directory: sub})
	if jerr != nil {
		t.Fatalf("Teleport: %v", jerr)
	}
	job = e.waitState(job.ID, protocol.StateFailed)
	if job.Error == nil || job.Error.Code != protocol.CodePathEscapesRoot {
		t.Fatalf("error = %+v, want path_escapes_root", job.Error)
	}
	if e.term.launchCount() != 0 {
		t.Error("nothing must be launched")
	}
	if len(e.harn.trusted) != 0 {
		t.Errorf("nothing must be trusted, got %v", e.harn.trusted)
	}
}

// A repository shipping `.claude` as a symbolic link to somewhere else must
// not get its worktree created there.
func TestWorktreeThroughSymlinkedClaudeDirIsRefused(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	checkout := filepath.Join(e.root, "wrekin")
	initRepo(t, checkout, "git@github.com:plmercereau/wrekin.git")
	e.harn.setTrusted(checkout, true)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(checkout, ".claude")); err != nil {
		t.Fatal(err)
	}
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(8, "plmercereau/wrekin"))
	if jerr != nil {
		t.Fatalf("Teleport: %v", jerr)
	}
	job = e.waitState(job.ID, protocol.StateFailed)
	if job.Error == nil || job.Error.Code != protocol.CodePathEscapesRoot {
		t.Fatalf("error = %+v, want path_escapes_root", job.Error)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the outside directory must stay empty, got %v", entries)
	}
	if e.term.launchCount() != 0 {
		t.Error("nothing must be launched")
	}
}

// file:// repositories are refused unless the git runner allows them.
func TestTeleportRefusesLocalRepositoryByDefault(t *testing.T) {
	requireGit(t)
	e := newEnv(t, func(d *RunnerDeps) { d.Git = gitx.Runner{} })
	bare := bareRepo(t)
	job, jerr := e.runner.Teleport(context.Background(), protocol.TeleportParams{Harness: "claude-code", CloudSessionID: sessionID(9), Repository: "file://" + bare})
	if jerr != nil {
		t.Fatalf("Teleport: %v", jerr)
	}
	job = e.waitState(job.ID, protocol.StateFailed)
	if job.Error == nil || job.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("error = %+v, want invalid_params", job.Error)
	}
}

// A harness without credentials fails the job before anything is cloned or
// asked: approving a clone only to fail in the terminal is what happened on a
// machine where Claude Code had never been signed in.
func TestTeleportFailsEarlyWhenTheHarnessIsSignedOut(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	e.harn.signedOut = true
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(9, "plmercereau/absent"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	j := e.waitState(job.ID, protocol.StateFailed)
	if j.Error == nil || j.Error.Code != protocol.CodeHarnessUnavailable || !strings.Contains(j.Error.Message, "not signed in") {
		t.Fatalf("error = %+v", j.Error)
	}
	if j.Attention != nil || e.term.launchCount() != 0 {
		t.Errorf("nothing may be asked or launched: attention %+v, launches %d", j.Attention, e.term.launchCount())
	}
}

// A harness that cannot tell (an older Claude Code without `auth status`)
// does not block the teleport.
func TestTeleportGoesOnWhenTheSignInStatusIsUnknown(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	e.harn.signInErr = errors.New("unknown command auth")
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(10, "plmercereau/absent"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	e.waitState(job.ID, protocol.StateNeedsAttention)
}

// A harness waiting on its own setup screen (Claude Code's first-run theme
// picker, whose menu marker looks like its prompt) is not ready: the job
// says what it waits on and how to attach, unconfirmed, and does not wait out
// the ready timeout.
func TestLaunchReportsASetupScreenInsteadOfReady(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	checkout := filepath.Join(e.root, "wrekin")
	initRepo(t, checkout, "git@github.com:plmercereau/wrekin.git")
	e.harn.setTrusted(checkout, true)
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(11, "plmercereau/wrekin"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	launched := e.waitLaunched(1)
	e.term.setPane(launched.session, "Choose a theme SETUP\n ❯ READY-looking menu line")
	j := e.waitState(job.ID, protocol.StateReady)
	if j.Ready == nil || j.Ready.Confirmed {
		t.Fatalf("ready = %+v, want unconfirmed", j.Ready)
	}
	if !strings.Contains(j.Ready.Detail, "its fake setup") || !strings.Contains(j.Ready.Detail, "wrekin attach "+launched.session) {
		t.Errorf("detail = %q", j.Ready.Detail)
	}
}

// With Remote Control on, the session starts under it, named after the node
// and the session, and is confirmed once the harness reports it active.
func TestLaunchUnderRemoteControl(t *testing.T) {
	requireGit(t)
	e := newEnv(t, func(d *RunnerDeps) {
		d.RemoteControl = true
		d.NodeName = "fennec"
		d.RemoteControlGrace = 300 * time.Millisecond
	})
	checkout := filepath.Join(e.root, "wrekin")
	initRepo(t, checkout, "git@github.com:plmercereau/wrekin.git")
	e.harn.setTrusted(checkout, true)

	job, jerr := e.runner.Teleport(context.Background(), teleportParams(12, "plmercereau/wrekin"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	launched := e.waitLaunched(1)
	wantName := "fennec " + launched.session
	if strings.Join(launched.argv, " ") != "claude --teleport "+sessionID(12)+" --remote-control "+wantName {
		t.Errorf("argv %q", launched.argv)
	}
	e.term.setPane(launched.session, "READY\nRC-ON")
	j := e.waitState(job.ID, protocol.StateReady)
	if j.Ready == nil || !j.Ready.Confirmed || !strings.Contains(j.Ready.Detail, "Remote Control active") {
		t.Errorf("ready = %+v", j.Ready)
	}
	if j.Session == nil || j.Session.RemoteControl != wantName {
		t.Errorf("session = %+v", j.Session)
	}
}

// A prompt without remote control after the grace is still ready, and says
// how to turn it on.
func TestLaunchSaysWhenRemoteControlDoesNotComeUp(t *testing.T) {
	requireGit(t)
	e := newEnv(t, func(d *RunnerDeps) {
		d.RemoteControl = true
		d.NodeName = "bad!name"
		d.RemoteControlGrace = 200 * time.Millisecond
	})
	checkout := filepath.Join(e.root, "wrekin")
	initRepo(t, checkout, "git@github.com:plmercereau/wrekin.git")
	e.harn.setTrusted(checkout, true)
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(13, "plmercereau/wrekin"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	launched := e.waitLaunched(1)
	// A node name the harness refuses falls back to the session's own name.
	if launched.argv[len(launched.argv)-1] != launched.session {
		t.Errorf("argv %q", launched.argv)
	}
	e.term.setPane(launched.session, "READY")
	j := e.waitState(job.ID, protocol.StateReady)
	if j.Ready == nil || !strings.Contains(j.Ready.Detail, "/remote-control") {
		t.Errorf("ready = %+v", j.Ready)
	}
}

// `wrekin kill` ends a session by job id or by session name; its job ends
// as session_gone, so teleporting again needs relaunch.
func TestKillEndsTheSessionAndTheJob(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	checkout := filepath.Join(e.root, "wrekin")
	initRepo(t, checkout, "git@github.com:plmercereau/wrekin.git")
	e.harn.setTrusted(checkout, true)

	for i, byJobID := range []bool{true, false} {
		job, jerr := e.runner.Teleport(context.Background(), teleportParams(20+i, "plmercereau/wrekin"))
		if jerr != nil {
			t.Fatal(jerr)
		}
		launched := e.waitLaunched(i + 1)
		e.term.setPane(launched.session, "READY")
		e.waitState(job.ID, protocol.StateReady)

		target := launched.session
		if byJobID {
			target = job.ID
		}
		killed, err := e.runner.Kill(target)
		if err != nil {
			t.Fatalf("Kill(%s): %v", target, err)
		}
		if killed == nil || killed.State != protocol.StateFailed || killed.Error == nil || killed.Error.Code != protocol.CodeSessionGone {
			t.Errorf("Kill(%s) = %+v", target, killed)
		}
		if exists, _ := e.term.Exists(launched.session); exists {
			t.Errorf("session %s still exists", launched.session)
		}
		if _, jerr := e.runner.Teleport(context.Background(), teleportParams(20+i, "plmercereau/wrekin")); jerr == nil || jerr.Code != protocol.CodeSessionGone {
			t.Errorf("teleport again without relaunch = %v, want session_gone", jerr)
		}
	}
}

func TestKillRefusesWhatIsNotWrekins(t *testing.T) {
	e := newEnv(t, nil)
	for _, target := range []string{"main", "wrekin-absent", "job_unknown"} {
		if _, err := e.runner.Kill(target); !errors.Is(err, ErrNothingToKill) {
			t.Errorf("Kill(%q) = %v, want ErrNothingToKill", target, err)
		}
	}
}

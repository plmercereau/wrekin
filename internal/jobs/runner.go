package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/harness"
	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/terminal"
	"github.com/plmercereau/wrekin/internal/workspace"
)

// RunnerDeps are the collaborators of a Runner.
type RunnerDeps struct {
	Store         *Store
	Policy        *workspace.Policy
	Git           gitx.Runner
	Term          terminal.Terminal
	Harness       harness.Harness
	MaxConcurrent int
	// ReadyTimeout bounds the wait for the harness prompt (default 120 s).
	ReadyTimeout time.Duration
	// PollInterval is how often the pane is inspected (default 2 s).
	PollInterval time.Duration
	Now          func() time.Time
	Logger       *slog.Logger
}

// Runner drives the job state machine. Every transition is persisted before
// it is observable.
type Runner struct {
	deps RunnerDeps

	mu sync.Mutex // serialises transitions and the idempotence checks
	wg sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc

	detectMu   sync.Mutex
	detectedAt time.Time
	detected   harness.Info
}

const (
	defaultReadyTimeout = 120 * time.Second
	defaultPollInterval = 2 * time.Second
	detectCacheTTL      = time.Minute
	worktreeExclude     = ".claude/worktrees/"
	reasonClone         = "clone"
	reasonTrust         = "trust"
)

// cloudSessionIDRe is the shape of a Claude Code Cloud session id (the MCP
// side checks the same prefix). The fixed prefix is what keeps a relay-chosen
// id from being read as a flag by the harness or as an option by tmux/git.
var cloudSessionIDRe = regexp.MustCompile(`^session_[A-Za-z0-9]{1,190}$`)

// NewRunner builds a runner; call Close to stop background work.
func NewRunner(deps RunnerDeps) *Runner {
	if deps.MaxConcurrent <= 0 {
		deps.MaxConcurrent = 4
	}
	if deps.ReadyTimeout <= 0 {
		deps.ReadyTimeout = defaultReadyTimeout
	}
	if deps.PollInterval <= 0 {
		deps.PollInterval = defaultPollInterval
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{deps: deps, ctx: ctx, cancel: cancel}
}

// Close cancels background preparations and waits for them.
func (r *Runner) Close() {
	r.cancel()
	r.wg.Wait()
}

func (r *Runner) now() time.Time { return r.deps.Now().UTC().Truncate(time.Second) }

func jobErr(code, format string, args ...any) *protocol.JobError {
	return &protocol.JobError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Teleport validates the request, applies the idempotence rules and starts a
// new job when one is needed. It returns within a second.
func (r *Runner) Teleport(ctx context.Context, p protocol.TeleportParams) (*Job, *protocol.JobError) {
	if p.Harness == "" || p.Harness != r.deps.Harness.ID() {
		return nil, jobErr(protocol.CodeInvalidParams, "harness %q is not supported by this node", p.Harness)
	}
	if !cloudSessionIDRe.MatchString(p.CloudSessionID) {
		return nil, jobErr(protocol.CodeInvalidParams, "cloudSessionId is missing or malformed")
	}
	if strings.TrimSpace(p.Repository) == "" && strings.TrimSpace(p.Directory) == "" {
		return nil, jobErr(protocol.CodeRepositoryRequired, "either repository or directory is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.deps.Store.FindBySession(p.Harness, p.CloudSessionID); ok {
		switch existing.State {
		case protocol.StateReady:
			alive := false
			if existing.Session != nil {
				alive, _ = r.deps.Term.Exists(existing.Session.Name)
			}
			if alive {
				return existing, nil
			}
			existing.Error = jobErr(protocol.CodeSessionGone, "the terminal session of this teleport has disappeared; request again with relaunch to start a new one")
			existing.Ready = nil
			r.transition(existing, protocol.StateFailed)
			if !p.Relaunch {
				return nil, existing.Error
			}
		case protocol.StateFailed:
			if !p.Relaunch {
				if existing.Error != nil && existing.Error.Code == protocol.CodeSessionGone {
					return nil, existing.Error
				}
				return existing, nil
			}
		default: // accepted, preparing, needs_attention, launching: in flight
			return existing, nil
		}
	}

	info := r.detect()
	if !info.Available {
		return nil, jobErr(protocol.CodeHarnessUnavailable, "the %s harness was not found on this machine", p.Harness)
	}
	if err := r.deps.Term.Available(); err != nil {
		return nil, jobErr(protocol.CodeTerminalUnavailable, "the terminal multiplexer is not available on this machine")
	}
	if r.activeCount() >= r.deps.MaxConcurrent {
		return nil, jobErr(protocol.CodeConcurrencyLimit, "%d teleport preparations are already running on this node", r.deps.MaxConcurrent)
	}

	now := r.now()
	job := &Job{
		Job: protocol.Job{
			ID:             newJobID(),
			Harness:        p.Harness,
			CloudSessionID: p.CloudSessionID,
			State:          protocol.StateAccepted,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		Params: p,
	}
	if err := r.deps.Store.Put(job); err != nil {
		r.deps.Logger.Error("persist job", "job", job.ID, "err", err)
		return nil, jobErr(protocol.CodeInternal, "the job could not be recorded")
	}
	r.deps.Logger.Info("job accepted", "job", job.ID, "cloudSession", p.CloudSessionID)
	r.spawn(func() { r.prepare(job.ID) })
	return job.clone(), nil
}

// Status returns a job.
func (r *Runner) Status(jobID string) (*Job, *protocol.JobError) {
	job, ok := r.deps.Store.Get(jobID)
	if !ok {
		return nil, jobErr(protocol.CodeUnknownJob, "no job of that id")
	}
	return job, nil
}

// List returns all jobs, newest first.
func (r *Runner) List() ([]*Job, *protocol.JobError) {
	return r.deps.Store.List(), nil
}

// Approve records the user's decision on a job waiting in needs_attention.
func (r *Runner) Approve(jobID string, approved bool) (*Job, *protocol.JobError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.deps.Store.Get(jobID)
	if !ok {
		return nil, jobErr(protocol.CodeUnknownJob, "no job of that id")
	}
	if job.State != protocol.StateNeedsAttention {
		return nil, jobErr(protocol.CodeAttentionNotPending, "this job is not waiting for a decision")
	}
	job.Attention = nil
	if !approved {
		job.Error = jobErr(protocol.CodeNotApproved, "the repository was not approved")
		r.transition(job, protocol.StateFailed)
		return job.clone(), nil
	}
	now := r.now()
	job.ApprovedAt = &now
	r.transition(job, protocol.StatePreparing)
	r.spawn(func() { r.resume(job.ID) })
	return job.clone(), nil
}

// Recover is called once at start: jobs interrupted by a daemon restart are
// failed; ready jobs are left alone until the next request checks them.
func (r *Runner) Recover() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range r.deps.Store.List() {
		switch job.State {
		case protocol.StateAccepted, protocol.StatePreparing, protocol.StateLaunching:
			job.Error = jobErr(protocol.CodeInternal, "the daemon restarted during preparation; request the teleport again with relaunch")
			job.Attention = nil
			r.transition(job, protocol.StateFailed)
			r.deps.Logger.Warn("job failed by recovery", "job", job.ID)
		}
	}
}

func (r *Runner) spawn(fn func()) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		fn()
	}()
}

// activeCount counts the preparations in flight (caller holds r.mu).
func (r *Runner) activeCount() int {
	n := 0
	for _, j := range r.deps.Store.List() {
		switch j.State {
		case protocol.StateAccepted, protocol.StatePreparing, protocol.StateLaunching:
			n++
		}
	}
	return n
}

// detect caches a successful harness detection for a minute; a missing
// harness is re-checked on every request so that installing it takes effect
// at once.
func (r *Runner) detect() harness.Info {
	r.detectMu.Lock()
	defer r.detectMu.Unlock()
	if r.detected.Available && time.Since(r.detectedAt) < detectCacheTTL {
		return r.detected
	}
	info, err := r.deps.Harness.Detect()
	if err != nil {
		r.deps.Logger.Warn("harness detection failed", "err", err)
		info = harness.Info{}
	}
	r.detected, r.detectedAt = info, time.Now()
	return info
}

// transition sets the state, stamps updatedAt and persists (caller holds
// r.mu or is the only goroutine working on the job).
func (r *Runner) transition(job *Job, state protocol.State) {
	job.State = state
	job.UpdatedAt = r.now()
	if err := r.deps.Store.Put(job); err != nil {
		r.deps.Logger.Error("persist job", "job", job.ID, "state", state, "err", err)
	}
	r.deps.Logger.Info("job state", "job", job.ID, "state", state)
}

// save persists the job under the lock.
func (r *Runner) save(job *Job, state protocol.State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transition(job, state)
}

// fail ends the job with an error code and a sentence; detail is logged
// locally only.
func (r *Runner) fail(job *Job, code, message string, detail error) {
	job.Error = &protocol.JobError{Code: code, Message: message}
	job.Attention = nil
	job.Ready = nil
	if detail != nil {
		r.deps.Logger.Warn("job failed", "job", job.ID, "code", code, "detail", detail.Error())
	} else {
		r.deps.Logger.Warn("job failed", "job", job.ID, "code", code)
	}
	r.save(job, protocol.StateFailed)
}

// prepare runs steps 2 to 6 of the state machine for a fresh job.
func (r *Runner) prepare(jobID string) {
	job, ok := r.deps.Store.Get(jobID)
	if !ok {
		return
	}
	r.save(job, protocol.StatePreparing)
	p := job.Params

	if dir := strings.TrimSpace(p.Directory); dir != "" {
		resolved, err := r.deps.Policy.Resolve(dir)
		switch {
		case errors.Is(err, workspace.ErrEscapesRoot):
			r.fail(job, protocol.CodePathEscapesRoot, "the directory resolves outside its allowed root through a symbolic link", err)
			return
		case err != nil:
			r.fail(job, protocol.CodeDirectoryNotAllowed, "the directory is outside every allowed directory", err)
			return
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			r.fail(job, protocol.CodeDirectoryNotAllowed, "the directory does not exist on this machine", err)
			return
		}
		if p.Repository != "" {
			if ref, err := workspace.ParseRepositoryWith(p.Repository, r.parseOptions()); err == nil {
				job.Repository = &protocol.Repository{Input: ref.Input, URL: ref.URL, Name: ref.Name}
			}
		}
		if r.deps.Git.IsRepo(resolved) {
			top, err := r.deps.Git.TopLevel(resolved)
			if err != nil {
				top = resolved
			}
			// The directory passed the policy; its repository's top level may
			// not (an allowed root inside a larger work tree, the home directory
			// kept as a repository). Nothing outside the roots is used.
			if _, err := r.deps.Policy.Resolve(top); err != nil {
				r.fail(job, protocol.CodePathEscapesRoot, "the repository containing the directory lies outside the allowed directories", err)
				return
			}
			job.CheckoutDir = top
			if job.Repository == nil {
				if remotes, err := r.deps.Git.Remotes(top); err == nil && remotes["origin"] != "" {
					if ref, err := workspace.ParseRepositoryWith(remotes["origin"], workspace.ParseOptions{AllowLocal: true}); err == nil {
						job.Repository = &protocol.Repository{Input: ref.Input, URL: ref.URL, Name: ref.Name}
					}
				}
			}
		} else {
			job.Workdir = resolved
		}
		r.isolate(job)
		return
	}

	ref, err := workspace.ParseRepositoryWith(p.Repository, r.parseOptions())
	if err != nil {
		r.fail(job, protocol.CodeInvalidParams, "the repository reference is not understood", err)
		return
	}
	job.Repository = &protocol.Repository{Input: ref.Input, URL: ref.URL, Name: ref.Name}
	checkouts, err := r.deps.Git.FindCheckouts(r.deps.Policy.Roots, ref.URL, 3)
	if err != nil {
		r.deps.Logger.Warn("checkout discovery failed", "job", job.ID, "err", err)
	}
	if len(checkouts) > 0 {
		job.CheckoutDir = checkouts[0]
		r.isolate(job)
		return
	}
	job.ClonePath = r.deps.Policy.ProposeClonePath(ref.Name)
	job.AttentionReason = reasonClone
	job.Attention = &protocol.Attention{
		Kind:          protocol.AttentionApproveRepository,
		Message:       fmt.Sprintf("The repository %s is not checked out under an allowed directory. Approve to clone it into %s.", ref.URL, job.ClonePath),
		RepositoryURL: ref.URL,
		ProposedPath:  job.ClonePath,
	}
	r.save(job, protocol.StateNeedsAttention)
}

// resume continues an approved job.
func (r *Runner) resume(jobID string) {
	job, ok := r.deps.Store.Get(jobID)
	if !ok {
		return
	}
	switch job.AttentionReason {
	case reasonClone:
		job.AttentionReason = ""
		url := ""
		if job.Repository != nil {
			url = job.Repository.URL
		}
		if err := r.deps.Git.Clone(r.ctx, url, job.ClonePath); err != nil {
			if errors.Is(err, gitx.ErrPromptBlocked) {
				r.fail(job, protocol.CodeGitPromptBlocked, "git needed credentials it could only ask for interactively; configure a credential helper or an ssh key on this machine", err)
			} else {
				r.fail(job, protocol.CodeCloneFailed, "the repository could not be cloned; see the node log", err)
			}
			return
		}
		job.CreatedClone = true
		job.CheckoutDir = job.ClonePath
		r.isolate(job)
	case reasonTrust:
		job.AttentionReason = ""
		r.trustAndLaunch(job)
	default:
		r.fail(job, protocol.CodeInternal, "the job was waiting for an unknown reason", nil)
	}
}

// isolate creates the worktree (step 4) when the job works on a repository,
// then continues with trust and launch.
func (r *Runner) isolate(job *Job) {
	if job.CheckoutDir != "" {
		wt := filepath.Join(job.CheckoutDir, ".claude", "worktrees", SessionName(job.CloudSessionID))
		// A repository may ship `.claude` or `.claude/worktrees` as a symbolic
		// link; os.MkdirAll and `git worktree add` would follow it out of the
		// root. Refuse the link, then check the canonical path before anything
		// is created there.
		for _, dir := range []string{filepath.Join(job.CheckoutDir, ".claude"), filepath.Join(job.CheckoutDir, ".claude", "worktrees")} {
			if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
				r.fail(job, protocol.CodePathEscapesRoot, "the repository's .claude directory is a symbolic link; refusing to create the worktree there", nil)
				return
			}
		}
		canon, err := r.deps.Policy.Resolve(wt)
		if err != nil {
			r.fail(job, protocol.CodePathEscapesRoot, "the worktree path resolves outside the allowed directories", err)
			return
		}
		if _, err := os.Stat(wt); err == nil && r.deps.Git.IsRepo(wt) {
			r.deps.Logger.Info("reusing worktree", "job", job.ID, "path", wt)
		} else {
			if err := r.deps.Git.AddWorktree(r.ctx, job.CheckoutDir, wt, BranchName(job.CloudSessionID)); err != nil {
				r.fail(job, protocol.CodeWorktreeFailed, "the isolated worktree could not be created; see the node log", err)
				return
			}
			job.CreatedWorktree = true
		}
		if err := r.deps.Git.EnsureExcluded(job.CheckoutDir, worktreeExclude); err != nil {
			r.deps.Logger.Warn("exclude not written", "job", job.ID, "err", err)
		}
		job.Workdir = canon
	}
	r.trustAndLaunch(job)
}

// parseOptions mirrors the git runner's transport allowance.
func (r *Runner) parseOptions() workspace.ParseOptions {
	return workspace.ParseOptions{AllowLocal: r.deps.Git.AllowLocal}
}

// trustAndLaunch is step 5: make sure the harness trusts the workdir, or ask.
func (r *Runner) trustAndLaunch(job *Job) {
	trusted, err := r.deps.Harness.Trusted(job.Workdir)
	if err != nil {
		r.deps.Logger.Warn("trust lookup failed", "job", job.ID, "err", err)
	}
	if !trusted {
		checkoutTrusted := false
		if job.CheckoutDir != "" && job.CheckoutDir != job.Workdir {
			checkoutTrusted, _ = r.deps.Harness.Trusted(job.CheckoutDir)
		}
		if job.ApprovedAt == nil && !checkoutTrusted {
			job.AttentionReason = reasonTrust
			repoURL := ""
			if job.Repository != nil {
				repoURL = job.Repository.URL
			}
			job.Attention = &protocol.Attention{
				Kind:          protocol.AttentionApproveRepository,
				Message:       fmt.Sprintf("The directory %s is not yet trusted by the harness. Approve to trust it and launch the session.", job.Workdir),
				RepositoryURL: repoURL,
				ProposedPath:  job.Workdir,
			}
			r.save(job, protocol.StateNeedsAttention)
			return
		}
		if err := r.deps.Harness.Trust(job.Workdir); err != nil {
			r.fail(job, protocol.CodeInternal, "the harness configuration could not be updated to trust the directory; run the harness once in it", err)
			return
		}
		if job.CheckoutDir != "" && job.CheckoutDir != job.Workdir && !checkoutTrusted {
			if err := r.deps.Harness.Trust(job.CheckoutDir); err != nil {
				r.deps.Logger.Warn("checkout trust not recorded", "job", job.ID, "err", err)
			}
		}
	}
	r.launch(job)
}

// launch is step 6: start the session and watch for the prompt.
func (r *Runner) launch(job *Job) {
	session := SessionName(job.CloudSessionID)
	if exists, _ := r.deps.Term.Exists(session); exists {
		r.deps.Logger.Warn("stale session killed before launch", "job", job.ID, "session", session)
		_ = r.deps.Term.Kill(session)
	}
	job.Session = &protocol.Session{Terminal: r.deps.Term.Name(), Name: session, Attach: "wrekin attach " + session}
	r.save(job, protocol.StateLaunching)

	argv := r.deps.Harness.LaunchArgs(job.CloudSessionID)
	if len(argv) == 0 {
		r.fail(job, protocol.CodeInvalidParams, "the harness refused the session id", nil)
		return
	}
	if err := r.deps.Term.Launch(r.ctx, session, job.Workdir, argv); err != nil {
		r.fail(job, protocol.CodeInternal, "the terminal session could not be started; see the node log", err)
		return
	}

	deadline := time.Now().Add(r.deps.ReadyTimeout)
	pattern := r.deps.Harness.ReadyPattern()
	stoppedPolls := 0
	for {
		exists, _ := r.deps.Term.Exists(session)
		running := false
		if exists {
			running, _ = r.deps.Term.Running(session)
		}
		if !exists || !running {
			stoppedPolls++
		} else {
			stoppedPolls = 0
		}
		if !exists || stoppedPolls >= 2 {
			if r.deps.Harness.ExitedIsFailure() {
				job.Session = nil
				r.fail(job, protocol.CodeHarnessExited, "the harness exited before its prompt appeared; attach to the node and run it by hand to see why", nil)
				return
			}
			job.Ready = &protocol.Ready{Confirmed: true, Detail: "the harness finished"}
			r.save(job, protocol.StateReady)
			return
		}
		if pane, err := r.deps.Term.Capture(session, 40); err == nil && pattern.MatchString(pane) {
			job.Ready = &protocol.Ready{Confirmed: true, Detail: "prompt visible"}
			r.save(job, protocol.StateReady)
			return
		}
		if time.Now().After(deadline) {
			job.Ready = &protocol.Ready{
				Confirmed: false,
				Detail:    fmt.Sprintf("the harness is running but its prompt was not seen within %s; attach to check", formatDuration(r.deps.ReadyTimeout)),
			}
			r.save(job, protocol.StateReady)
			return
		}
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(r.deps.PollInterval):
		}
	}
}

func formatDuration(d time.Duration) string {
	if d >= time.Second && d%time.Second == 0 {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return d.String()
}

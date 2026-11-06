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
	"unicode"

	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/harness"
	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/repos"
	"github.com/plmercereau/wrekin/internal/terminal"
	"github.com/plmercereau/wrekin/internal/workspace"
)

// RunnerDeps are the collaborators of a Runner.
type RunnerDeps struct {
	Store  *Store
	Policy *workspace.Policy
	// Declared are the repository pins of settings.json
	// (workspace.repositories): repository reference to directory, "~"
	// already expanded. A pin wins over everything else; an invalid pin fails
	// the job.
	Declared map[string]string
	// Learned holds the mappings the daemon found or cloned; nil disables it.
	Learned       *repos.Store
	Git           gitx.Runner
	Term          terminal.Terminal
	Harness       harness.Harness
	MaxConcurrent int
	// RemoteControl launches sessions under the harness's remote control
	// (settings teleport.remoteControl), listed as "<NodeName> <session>".
	RemoteControl bool
	NodeName      string
	// RemoteControlGrace is how long, once the prompt shows, the runner waits
	// for remote control to report itself active (default 20 s).
	RemoteControlGrace time.Duration
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
	// defaultRemoteControlGrace: Claude Code 2.1.177 shows "/rc active" a few
	// seconds after its prompt.
	defaultRemoteControlGrace = 20 * time.Second
	detectCacheTTL            = time.Minute
	worktreeExclude           = ".claude/worktrees/"
	reasonClone               = "clone"
	reasonTrust               = "trust"
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
	if deps.RemoteControlGrace <= 0 {
		deps.RemoteControlGrace = defaultRemoteControlGrace
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

// ErrNothingToKill means Kill found neither a job nor a Wrekin session of
// that name.
var ErrNothingToKill = errors.New("no teleport job or session of that name")

// Kill ends a teleported session from the node: target is a job id or a
// session name (wrekin-…). The terminal session is killed and the job, if
// any, ends in failed/session_gone, so a later teleport of the same cloud
// session needs relaunch, as after any lost session. The worktree stays;
// `wrekin cleanup` removes it. It returns the job, or nil for a session
// without one (left by an older daemon).
func (r *Runner) Kill(target string) (*Job, error) {
	target = strings.TrimSpace(target)
	r.mu.Lock()
	defer r.mu.Unlock()
	var job *Job
	if j, ok := r.deps.Store.Get(target); ok {
		job = j
	} else {
		for _, j := range r.deps.Store.List() {
			if j.Session != nil && j.Session.Name == target {
				job = j
				break
			}
		}
	}
	session := target
	if job != nil {
		if job.Session == nil {
			return nil, fmt.Errorf("job %s has no session (state %s)", job.ID, job.State)
		}
		session = job.Session.Name
	} else if !strings.HasPrefix(target, sessionPrefix) {
		return nil, ErrNothingToKill
	}
	exists, err := r.deps.Term.Exists(session)
	if err != nil {
		return nil, err
	}
	if !exists && job == nil {
		return nil, ErrNothingToKill
	}
	if exists {
		if err := r.deps.Term.Kill(session); err != nil {
			return nil, err
		}
	}
	r.deps.Logger.Info("session killed", "session", session)
	if job == nil {
		return nil, nil
	}
	if job.State == protocol.StateReady || job.State == protocol.StateLaunching {
		job.Ready = nil
		job.Error = jobErr(protocol.CodeSessionGone, "the session was ended on the node (wrekin kill); request again with relaunch to start a new one")
		r.transition(job, protocol.StateFailed)
	}
	return job.clone(), nil
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

	// Before anything is cloned or asked: a harness without credentials would
	// only fail later, in the terminal, after the user approved a clone.
	signedIn, err := r.deps.Harness.SignedIn(r.ctx)
	switch {
	case err != nil:
		r.deps.Logger.Warn("harness sign-in status unknown; going on", "job", job.ID, "err", err)
	case !signedIn:
		r.fail(job, protocol.CodeHarnessUnavailable,
			fmt.Sprintf("%s is not signed in on this machine: sign in there once (for Claude Code, `claude auth login`), then request the teleport again", job.Harness), nil)
		return
	}

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
	checkout, ok := r.locate(job, ref)
	if !ok {
		return
	}
	if checkout != "" {
		job.CheckoutDir = checkout
		r.isolate(job)
		return
	}
	job.ClonePath = r.deps.Policy.ProposeClonePath(ref.Name)
	job.AttentionReason = reasonClone
	// One approval covers the clone and the trust the harness needs to run in
	// it (ApprovedAt skips the trust prompt), so the message names both.
	job.Attention = &protocol.Attention{
		Kind:          protocol.AttentionApproveRepository,
		Message:       fmt.Sprintf("The repository %s is not checked out under an allowed directory. Approve to clone it into %s and to mark it as trusted for Claude Code (no permission bypass).", ref.URL, job.ClonePath),
		RepositoryURL: ref.URL,
		ProposedPath:  job.ClonePath,
	}
	r.save(job, protocol.StateNeedsAttention)
}

// locate finds an existing checkout of ref, in order: the pin declared in
// the settings, the mapping learned earlier, the discovery scan. "" means
// none was found (a clone is to be proposed); ok is false when the job has
// failed (an invalid pin).
func (r *Runner) locate(job *Job, ref workspace.RepoRef) (dir string, ok bool) {
	if pinned, found := r.pinned(ref.URL); found {
		top, err := repos.Verify(r.deps.Policy, r.deps.Git, pinned, ref.URL)
		if err != nil {
			r.failPin(job, ref.URL, err)
			return "", false
		}
		r.deps.Logger.Info("using declared checkout", "job", job.ID, "path", top)
		return top, true
	}
	if r.deps.Learned != nil {
		entry, found, err := r.deps.Learned.Lookup(ref.URL)
		switch {
		case err != nil:
			r.deps.Logger.Warn("learned mappings unreadable", "job", job.ID, "err", err)
		case found:
			top, err := repos.Verify(r.deps.Policy, r.deps.Git, entry.Path, ref.URL)
			if err == nil {
				if err := r.deps.Learned.Touch(ref.URL, r.now()); err != nil {
					r.deps.Logger.Warn("learned mapping not updated", "job", job.ID, "err", err)
				}
				r.deps.Logger.Info("using learned checkout", "job", job.ID, "path", top)
				return top, true
			}
			r.deps.Logger.Info("learned mapping no longer holds; forgetting it", "job", job.ID, "path", entry.Path, "reason", err.Error())
			if _, _, err := r.deps.Learned.Forget(ref.URL); err != nil {
				r.deps.Logger.Warn("learned mapping not forgotten", "job", job.ID, "err", err)
			}
		}
	}
	checkouts, err := r.deps.Git.FindCheckouts(r.deps.Policy.Roots, ref.URL, 3)
	if err != nil {
		r.deps.Logger.Warn("checkout discovery failed", "job", job.ID, "err", err)
	}
	if len(checkouts) == 0 {
		return "", true
	}
	r.learn(job, ref.URL, checkouts[0], repos.SourceDiscovered)
	return checkouts[0], true
}

// pinned returns the directory declared for repo, whatever spelling the
// settings use.
func (r *Runner) pinned(repo string) (string, bool) {
	key, ok := workspace.RepoKey(repo)
	if !ok {
		return "", false
	}
	for declared, dir := range r.deps.Declared {
		if k, ok := workspace.RepoKey(declared); ok && k == key {
			return dir, true
		}
	}
	return "", false
}

// failPin ends a job whose declared checkout cannot be used. A pin is an
// explicit decision: the job does not fall back to a learned mapping or a
// scan. The message names the repository, not the local path.
func (r *Runner) failPin(job *Job, repo string, err error) {
	where := fmt.Sprintf("the checkout declared for %s in settings.json (workspace.repositories)", repo)
	problem := repos.Problem("")
	var ve *repos.VerifyError
	if errors.As(err, &ve) {
		problem = ve.Problem
	}
	switch problem {
	case repos.ProblemEscapesRoot:
		r.fail(job, protocol.CodePathEscapesRoot, where+" resolves outside its allowed directory through a symbolic link", err)
	case repos.ProblemNotAllowed:
		r.fail(job, protocol.CodeDirectoryNotAllowed, where+" is outside every allowed directory", err)
	case repos.ProblemMissing:
		r.fail(job, protocol.CodeDirectoryNotAllowed, where+" does not exist on this machine", err)
	case repos.ProblemNotRepository:
		r.fail(job, protocol.CodeInvalidParams, where+" is not a git repository", err)
	default:
		r.fail(job, protocol.CodeInvalidParams, where+" is a checkout of another repository (its origin does not match)", err)
	}
}

// learn records a mapping found or made for a job; a failure is only logged.
func (r *Runner) learn(job *Job, repo, dir string, source repos.Source) {
	if r.deps.Learned == nil {
		return
	}
	if err := r.deps.Learned.Record(repo, dir, source, r.now()); err != nil {
		r.deps.Logger.Warn("mapping not learned", "job", job.ID, "path", dir, "err", err)
	}
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
		if job.Repository != nil {
			clone := job.ClonePath
			if canon, err := filepath.EvalSymlinks(clone); err == nil {
				clone = canon
			}
			r.learn(job, job.Repository.URL, clone, repos.SourceCloned)
		}
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
	// Remote control is turned on once the session is up, by typing the
	// harness's command into it, under "<node> <session>" (or the session's
	// name alone when the harness refuses the node's).
	rcName, rcCommand := "", ""
	if r.deps.RemoteControl {
		candidates := []string{strings.TrimSpace(r.deps.NodeName + " " + session), session}
		if title := remoteControlTitle(job.Params.Title); title != "" {
			named := title
			if r.deps.NodeName != "" {
				named += " (" + r.deps.NodeName + ")"
			}
			candidates = append([]string{named}, candidates...)
		}
		for _, name := range candidates {
			if cmd := r.deps.Harness.RemoteControlCommand(name); cmd != "" {
				rcName, rcCommand = name, cmd
				break
			}
		}
	}
	job.Session = &protocol.Session{Terminal: r.deps.Term.Name(), Name: session, Attach: "wrekin attach " + session, RemoteControl: rcName}
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

	// Once the session is up, a pane whose program exits goes with it again,
	// as the user expects of a terminal (/exit closes the session).
	defer func() {
		if job.State == protocol.StateReady {
			_ = r.deps.Term.KeepOnExit(session, false)
		}
	}()

	deadline := time.Now().Add(r.deps.ReadyTimeout)
	pattern := r.deps.Harness.ReadyPattern()
	stoppedPolls := 0
	var promptSince time.Time
	rcSent := false
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
				// The pane stayed with the harness's last words (KeepOnExit):
				// they say why, which "it exited" alone never did.
				message := "the harness exited before its prompt appeared; attach to the node and run it by hand to see why"
				if exists {
					if pane, err := r.deps.Term.Capture(session, 40); err == nil {
						if reason := r.deps.Harness.ExitReason(r.ctx, pane); reason != "" {
							message = "the harness exited before its prompt appeared. " + reason
						}
					}
					_ = r.deps.Term.Kill(session)
				}
				job.Session = nil
				r.fail(job, protocol.CodeHarnessExited, message, nil)
				return
			}
			job.Ready = &protocol.Ready{Confirmed: true, Detail: "the harness finished"}
			r.save(job, protocol.StateReady)
			return
		}
		if pane, err := r.deps.Term.Capture(session, 40); err == nil {
			// A setup screen first: its menus are drawn with the same "❯" as the prompt.
			if what := r.deps.Harness.SetupScreen(pane); what != "" {
				job.Ready = &protocol.Ready{
					Confirmed: false,
					Detail: fmt.Sprintf("the harness is waiting on %s, not in the session yet: attach with `%s` and finish it there; the teleported session opens right after",
						what, job.Session.Attach),
				}
				r.save(job, protocol.StateReady)
				return
			}
			if pattern.MatchString(pane) {
				var ready *protocol.Ready
				active, url := false, ""
				if rcCommand != "" {
					active, url = r.deps.Harness.RemoteControl(pane)
				}
				switch {
				case rcCommand == "":
					ready = &protocol.Ready{Confirmed: true, Detail: "prompt visible"}
				case active && (url != "" || rcSent && time.Since(promptSince) >= r.deps.RemoteControlGrace):
					job.Session.RemoteControlURL = url
					ready = &protocol.Ready{Confirmed: true, Detail: "prompt visible, Remote Control active"}
				default:
					if promptSince.IsZero() {
						promptSince = time.Now()
					}
					// Typed only into the bare input box: a dialog in front of it
					// (an upsell, a tip) would take the line as its answer.
					if !rcSent && r.deps.Harness.InputIdle(pane) {
						if err := r.deps.Term.SendLine(session, rcCommand); err != nil {
							r.deps.Logger.Warn("remote control command not sent", "job", job.ID, "err", err)
						} else {
							r.deps.Logger.Info("remote control requested", "job", job.ID, "name", rcName)
						}
						rcSent = true
						promptSince = time.Now()
					}
					if time.Since(promptSince) >= r.deps.RemoteControlGrace || !time.Now().Before(deadline) {
						job.Session.RemoteControl = ""
						why := "it did not come up"
						if !rcSent {
							why = "a dialog stayed in front of the input"
						}
						ready = &protocol.Ready{Confirmed: true, Detail: fmt.Sprintf("prompt visible, but Remote Control is off (%s): attach with `%s` and run /remote-control there", why, job.Session.Attach)}
					}
				}
				if ready != nil {
					job.Ready = ready
					r.save(job, protocol.StateReady)
					return
				}
			}
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

// remoteControlTitle makes the cloud session's title, which Claude sends,
// fit for a Remote Control name: letters, digits and common punctuation on
// one line, other characters as spaces, at most 72 characters, starting with
// a letter or a digit. "" when nothing is left.
func remoteControlTitle(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), strings.ContainsRune("._,:;'’!?()&+/#-", r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimLeftFunc(out, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if runes := []rune(out); len(runes) > 72 {
		out = strings.TrimSpace(string(runes[:72]))
	}
	return out
}

func formatDuration(d time.Duration) string {
	if d >= time.Second && d%time.Second == 0 {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return d.String()
}

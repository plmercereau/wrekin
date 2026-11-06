// Package daemon wires the configuration, the identity, the job runner, the
// control socket and the relay client into the long-running node process.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plmercereau/wrekin/internal/config"
	"github.com/plmercereau/wrekin/internal/control"
	"github.com/plmercereau/wrekin/internal/gitx"
	"github.com/plmercereau/wrekin/internal/harness"
	"github.com/plmercereau/wrekin/internal/harness/claude"
	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/jobs"
	"github.com/plmercereau/wrekin/internal/paths"
	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/relay"
	"github.com/plmercereau/wrekin/internal/repos"
	"github.com/plmercereau/wrekin/internal/terminal"
	"github.com/plmercereau/wrekin/internal/workspace"
)

// Options configures Run.
type Options struct {
	SettingsPath string
	Version      string
	// Stderr receives a copy of the log; nil means os.Stderr.
	Stderr io.Writer
}

const (
	logMaxSize = 10 << 20
	logKeep    = 3
)

// Run starts the daemon and blocks until ctx is done or a fatal error.
func Run(ctx context.Context, o Options) error {
	settings, err := config.Load(o.SettingsPath)
	if err != nil {
		return err
	}
	id, err := identity.Load(paths.IdentityFile())
	if err != nil {
		return err
	}
	if !id.Paired() {
		return identity.ErrNotPaired
	}

	logWriter, err := newRotatingWriter(paths.LogFile(), logMaxSize, logKeep)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer logWriter.Close()
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(logWriter, stderr), &slog.HandlerOptions{Level: logLevel(settings.Logging.Level)}))
	logger.Info("wrekin daemon starting", "version", o.Version, "node", id.NodeID, "name", settings.Name)

	if err := os.MkdirAll(settings.Workspace.DefaultDirectory, 0o755); err != nil {
		return fmt.Errorf("create default directory: %w", err)
	}
	policy, err := workspace.NewPolicy(settings.Workspace.AllowedDirectories, settings.Workspace.DefaultDirectory)
	if err != nil {
		return err
	}
	store, err := jobs.Open(paths.JobsFile())
	if err != nil {
		return err
	}
	term, err := terminal.Lookup(settings.Teleport.Terminal)
	if err != nil {
		return err
	}
	registry := harness.NewRegistry(claude.New("", "claude"))
	harn, _ := registry.Get(claude.ID)

	learned := repos.New(paths.RepositoriesFile())
	runner := jobs.NewRunner(jobs.RunnerDeps{
		Store:         store,
		Policy:        policy,
		Declared:      settings.Workspace.Repositories,
		Learned:       learned,
		Git:           gitx.Runner{},
		Term:          term,
		Harness:       harn,
		MaxConcurrent: settings.Teleport.MaxConcurrent,
		Logger:        logger,
	})
	defer runner.Close()
	runner.Recover()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var harnesses []protocol.HarnessInfo
	for hid, info := range registry.DetectAll(ctx) {
		harnesses = append(harnesses, protocol.HarnessInfo{ID: hid, Available: info.Available, Version: info.Version})
	}
	client := relay.New(relay.Options{
		URL:          settings.Relay.URL,
		Identity:     id,
		Name:         settings.Name,
		AgentVersion: o.Version,
		Harnesses:    harnesses,
		Handler:      NewHandler(runner, nil),
		Logger:       logger,
	})

	d := &daemon{
		version: o.Version, startedAt: time.Now(), id: id, settings: settings,
		store: store, learned: learned, runner: runner, term: term, git: gitx.Runner{}, policy: policy,
		client: client, logger: logger, shutdown: cancel,
	}
	ln, err := control.Listen(paths.SocketFile())
	if err != nil {
		return fmt.Errorf("control socket: %w", err)
	}
	defer os.Remove(paths.SocketFile())
	controlErr := make(chan error, 1)
	go func() { controlErr <- control.NewServer(d.handleControl).Serve(ctx, ln) }()

	relayErr := make(chan error, 1)
	go func() { relayErr <- client.Run(ctx) }()

	select {
	case <-ctx.Done():
		logger.Info("wrekin daemon stopping")
		return nil
	case err := <-controlErr:
		if err != nil {
			return fmt.Errorf("control socket: %w", err)
		}
		return nil
	case err := <-relayErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("relay: %w", err)
		}
		return nil
	}
}

func logLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// daemon holds the running components for the control handlers.
type daemon struct {
	version   string
	startedAt time.Time
	id        *identity.Identity
	settings  config.Settings
	store     *jobs.Store
	learned   *repos.Store
	runner    *jobs.Runner
	term      terminal.Terminal
	git       gitx.Runner
	policy    *workspace.Policy
	client    *relay.Client
	logger    *slog.Logger
	shutdown  context.CancelFunc
}

func (d *daemon) handleControl(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case control.MethodStatus:
		counts := map[string]int{}
		for _, j := range d.store.List() {
			counts[string(j.State)]++
		}
		return control.StatusInfo{
			Version: d.version, PID: os.Getpid(), StartedAt: d.startedAt,
			NodeID: d.id.NodeID, Name: d.settings.Name, AccountID: d.id.AccountID,
			Relay: d.client.State(), Jobs: counts,
		}, nil
	case control.MethodSessions:
		out := control.SessionsResult{Jobs: []protocol.Job{}}
		for _, j := range d.store.List() {
			out.Jobs = append(out.Jobs, j.Wire())
		}
		return out, nil
	case control.MethodCleanup:
		var p control.CleanupParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, fmt.Errorf("cleanup parameters: %w", err)
			}
		}
		return d.cleanup(ctx, p.DryRun), nil
	case control.MethodReposForget:
		var p control.ReposForgetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("repos.forget parameters: %w", err)
		}
		entry, removed, err := d.learned.Forget(p.Repository)
		if err != nil {
			return nil, err
		}
		if removed {
			d.logger.Info("learned mapping forgotten", "repository", entry.Repository, "path", entry.Path)
		}
		return control.ReposForgetResult{Removed: removed, Repository: entry.Repository, Path: entry.Path}, nil
	case control.MethodShutdown:
		d.logger.Info("shutdown requested through the control socket")
		go func() {
			time.Sleep(100 * time.Millisecond)
			d.shutdown()
		}()
		return map[string]bool{"stopping": true}, nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

// cleanup removes what Wrekin created for finished jobs: tmux sessions,
// worktrees it added and clones it made. Repositories that existed before are
// never touched; jobs still in flight are skipped. Every recorded path is
// re-checked against the policy first: jobs.json is written only by the
// daemon, but a bug there must not become the removal of a user directory.
// A refused path is reported under Errors and left alone.
func (d *daemon) cleanup(ctx context.Context, dryRun bool) control.CleanupReport {
	report := control.CleanupReport{DryRun: dryRun, Sessions: []string{}, Worktrees: []string{}, Clones: []string{}, Jobs: []string{}, Skipped: []string{}, Errors: []string{}}
	for _, j := range d.store.List() {
		switch j.State {
		case protocol.StateAccepted, protocol.StatePreparing, protocol.StateLaunching:
			report.Skipped = append(report.Skipped, j.ID+" ("+string(j.State)+")")
			continue
		}
		if j.Session != nil {
			if exists, _ := d.term.Exists(j.Session.Name); exists {
				report.Sessions = append(report.Sessions, j.Session.Name)
				if !dryRun {
					if err := d.term.Kill(j.Session.Name); err != nil {
						report.Errors = append(report.Errors, fmt.Sprintf("session %s: %v", j.Session.Name, err))
					}
				}
			}
		}
		if j.CreatedWorktree && j.Workdir != "" && j.CheckoutDir != "" {
			checkout, workdir, err := d.worktreeToRemove(j)
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("worktree %s: %v", j.Workdir, err))
			} else if _, err := os.Stat(workdir); err == nil {
				report.Worktrees = append(report.Worktrees, workdir)
				if !dryRun {
					if err := d.git.RemoveWorktree(ctx, checkout, workdir); err != nil {
						report.Errors = append(report.Errors, fmt.Sprintf("worktree %s: %v", workdir, err))
					}
				}
			}
		}
		if j.CreatedClone && j.CheckoutDir != "" {
			clone, err := d.cloneToRemove(j)
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("clone %s: %v", j.CheckoutDir, err))
			} else if _, err := os.Stat(clone); err == nil {
				report.Clones = append(report.Clones, clone)
				if !dryRun {
					if err := os.RemoveAll(clone); err != nil {
						report.Errors = append(report.Errors, fmt.Sprintf("clone %s: %v", clone, err))
					} else if d.learned != nil {
						if _, err := d.learned.ForgetPath(clone); err != nil {
							report.Errors = append(report.Errors, fmt.Sprintf("learned mapping of %s: %v", clone, err))
						}
					}
				}
			}
		}
		report.Jobs = append(report.Jobs, j.ID)
		if !dryRun {
			if err := d.store.Delete(j.ID); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("job %s: %v", j.ID, err))
			}
		}
	}
	if !dryRun {
		d.logger.Info("cleanup done", "sessions", len(report.Sessions), "worktrees", len(report.Worktrees), "clones", len(report.Clones), "jobs", len(report.Jobs))
	}
	return report
}

// worktreeToRemove re-checks a recorded worktree: the checkout and the
// worktree must both resolve under an allowed root, and the worktree must sit
// in the checkout's .claude/worktrees directory, the only place Wrekin creates
// one. It returns the canonical paths to hand to git.
func (d *daemon) worktreeToRemove(j *jobs.Job) (checkout, workdir string, err error) {
	checkout, err = d.policy.Resolve(j.CheckoutDir)
	if err != nil {
		return "", "", fmt.Errorf("checkout %s: %w", j.CheckoutDir, err)
	}
	workdir, err = d.policy.Resolve(j.Workdir)
	if err != nil {
		return "", "", err
	}
	worktrees := filepath.Join(checkout, ".claude", "worktrees")
	if !strings.HasPrefix(workdir, worktrees+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s is not under %s; refusing to remove it", workdir, worktrees)
	}
	return checkout, workdir, nil
}

// cloneToRemove re-checks a recorded clone: it must resolve under an allowed
// root and must not be a root itself, which os.RemoveAll would empty.
func (d *daemon) cloneToRemove(j *jobs.Job) (string, error) {
	clone, err := d.policy.Resolve(j.CheckoutDir)
	if err != nil {
		return "", err
	}
	if d.policy.IsRoot(clone) {
		return "", fmt.Errorf("%s is an allowed root; refusing to remove it", clone)
	}
	return clone, nil
}

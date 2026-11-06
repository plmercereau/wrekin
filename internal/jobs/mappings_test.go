package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/repos"
)

// twoCheckouts creates two checkouts of o/r; the discovery scan returns the
// "a-scanned" one first (its results are sorted), so a job landing in
// "z-mapped" proves a mapping was used.
func twoCheckouts(t *testing.T, e *env) (scanned, mapped string) {
	t.Helper()
	scanned = filepath.Join(e.root, "a-scanned")
	mapped = filepath.Join(e.root, "z-mapped")
	initRepo(t, scanned, "https://github.com/o/r")
	initRepo(t, mapped, "git@github.com:o/r.git")
	e.harn.setTrusted(scanned, true)
	e.harn.setTrusted(mapped, true)
	return scanned, mapped
}

func (e *env) checkoutOf(n int) *Job {
	e.t.Helper()
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(n, "o/r"))
	if jerr != nil {
		e.t.Fatal(jerr)
	}
	e.waitLaunched(1)
	j, _ := e.runner.Status(job.ID)
	return j
}

func (e *env) learnedEntry(repo string) (repos.Entry, bool) {
	e.t.Helper()
	entry, ok, err := e.learned.Lookup(repo)
	if err != nil {
		e.t.Fatal(err)
	}
	return entry, ok
}

func TestDeclaredPinIsUsed(t *testing.T) {
	requireGit(t)
	var e *env
	pins := map[string]string{}
	e = newEnv(t, func(d *RunnerDeps) { d.Declared = pins })
	_, mapped := twoCheckouts(t, e)
	pins["https://github.com/o/r.git"] = mapped
	if j := e.checkoutOf(1); j.CheckoutDir != mapped {
		t.Errorf("checkout %q, want the pinned %q", j.CheckoutDir, mapped)
	}
	// A pinned repository is not learned: the pin is its source of truth.
	if _, ok := e.learnedEntry("o/r"); ok {
		t.Error("a pinned repository must not be recorded as learned")
	}
}

func TestInvalidPinFailsTheJob(t *testing.T) {
	requireGit(t)
	outside := t.TempDir()
	cases := []struct {
		name string
		pin  func(e *env) string
		code string
	}{
		{"outside the roots", func(e *env) string { return outside }, protocol.CodeDirectoryNotAllowed},
		{"through a symbolic link", func(e *env) string {
			link := filepath.Join(e.root, "link")
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, protocol.CodePathEscapesRoot},
		{"missing", func(e *env) string { return filepath.Join(e.root, "gone") }, protocol.CodeDirectoryNotAllowed},
		{"not a repository", func(e *env) string {
			dir := filepath.Join(e.root, "plain")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return dir
		}, protocol.CodeInvalidParams},
		{"another origin", func(e *env) string {
			dir := filepath.Join(e.root, "other")
			initRepo(t, dir, "https://github.com/o/other")
			return dir
		}, protocol.CodeInvalidParams},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pins := map[string]string{}
			e := newEnv(t, func(d *RunnerDeps) { d.Declared = pins })
			twoCheckouts(t, e) // a valid checkout exists: no silent fallback to it
			pins["o/r"] = c.pin(e)
			job, jerr := e.runner.Teleport(context.Background(), teleportParams(20+i, "o/r"))
			if jerr != nil {
				t.Fatal(jerr)
			}
			failed := e.waitState(job.ID, protocol.StateFailed)
			if failed.Error == nil || failed.Error.Code != c.code {
				t.Fatalf("error = %+v, want %s", failed.Error, c.code)
			}
			if !strings.Contains(failed.Error.Message, "settings") || !strings.Contains(failed.Error.Message, "https://github.com/o/r") {
				t.Errorf("the message must point at the pin in the settings: %q", failed.Error.Message)
			}
			if strings.Contains(failed.Error.Message, outside) {
				t.Errorf("the message must not leak a local path: %q", failed.Error.Message)
			}
			if e.term.launchCount() != 0 {
				t.Error("nothing must be launched")
			}
		})
	}
}

func TestLearnedMappingIsUsedAndTouched(t *testing.T) {
	requireGit(t)
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(d *RunnerDeps) { d.Now = func() time.Time { return clock } })
	_, mapped := twoCheckouts(t, e)
	added := clock.Add(-24 * time.Hour)
	if err := e.learned.Record("https://github.com/o/r", mapped, repos.SourceCloned, added); err != nil {
		t.Fatal(err)
	}
	if j := e.checkoutOf(1); j.CheckoutDir != mapped {
		t.Errorf("checkout %q, want the learned %q", j.CheckoutDir, mapped)
	}
	entry, ok := e.learnedEntry("o/r")
	if !ok || entry.Path != mapped || entry.Source != repos.SourceCloned || !entry.AddedAt.Equal(added) || !entry.LastUsedAt.Equal(clock) {
		t.Errorf("entry = %+v, want lastUsedAt refreshed and the rest kept", entry)
	}
}

func TestStaleLearnedMappingIsDroppedThenScanned(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	scanned, _ := twoCheckouts(t, e)
	if err := e.learned.Record("o/r", filepath.Join(e.root, "moved-away"), repos.SourceDiscovered, time.Now()); err != nil {
		t.Fatal(err)
	}
	if j := e.checkoutOf(1); j.CheckoutDir != scanned {
		t.Errorf("checkout %q, want the scanned %q", j.CheckoutDir, scanned)
	}
	if entry, ok := e.learnedEntry("o/r"); !ok || entry.Path != scanned || entry.Source != repos.SourceDiscovered {
		t.Errorf("entry = %+v %v, want the stale one replaced by the scan result", entry, ok)
	}
}

func TestStaleLearnedMappingWithoutCheckoutIsForgotten(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	other := filepath.Join(e.root, "other")
	initRepo(t, other, "https://github.com/o/other")
	if err := e.learned.Record("o/r", other, repos.SourceDiscovered, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(1, "o/r"))
	if jerr != nil {
		t.Fatal(jerr)
	}
	e.waitState(job.ID, protocol.StateNeedsAttention)
	if entry, ok := e.learnedEntry("o/r"); ok {
		t.Errorf("the mapping whose origin changed must be dropped, still %+v", entry)
	}
}

func TestScanHitIsLearned(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	scanned, _ := twoCheckouts(t, e)
	e.checkoutOf(1)
	entry, ok := e.learnedEntry("git@github.com:o/r.git")
	if !ok || entry.Path != scanned || entry.Source != repos.SourceDiscovered || entry.Repository != "https://github.com/o/r" {
		t.Errorf("entry = %+v %v", entry, ok)
	}
}

func TestApprovedCloneIsLearned(t *testing.T) {
	requireGit(t)
	e := newEnv(t, nil)
	url := bareRepo(t)
	job, jerr := e.runner.Teleport(context.Background(), teleportParams(1, url))
	if jerr != nil {
		t.Fatal(jerr)
	}
	j := e.waitState(job.ID, protocol.StateNeedsAttention)
	if _, ok := e.learnedEntry(url); ok {
		t.Fatal("nothing is learned before the clone")
	}
	if _, jerr := e.runner.Approve(job.ID, true); jerr != nil {
		t.Fatal(jerr)
	}
	e.waitLaunched(1)
	entry, ok := e.learnedEntry(url)
	if !ok || entry.Path != j.Attention.ProposedPath || entry.Source != repos.SourceCloned || entry.Repository != url {
		t.Errorf("entry = %+v %v, want the clone at %s", entry, ok, j.Attention.ProposedPath)
	}
}

func TestPinBeatsLearned(t *testing.T) {
	requireGit(t)
	pins := map[string]string{}
	e := newEnv(t, func(d *RunnerDeps) { d.Declared = pins })
	scanned, mapped := twoCheckouts(t, e)
	pins["o/r"] = mapped
	if err := e.learned.Record("o/r", scanned, repos.SourceDiscovered, time.Now()); err != nil {
		t.Fatal(err)
	}
	if j := e.checkoutOf(1); j.CheckoutDir != mapped {
		t.Errorf("checkout %q, want the pinned %q", j.CheckoutDir, mapped)
	}
	if entry, ok := e.learnedEntry("o/r"); !ok || entry.Path != scanned {
		t.Errorf("the shadowed learned entry must be kept as is, got %+v %v", entry, ok)
	}
}

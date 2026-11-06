// Package repos keeps the repository mappings the daemon learned: which
// directory holds the checkout of which repository. They complement the
// mappings declared in settings.json (workspace.repositories) the way
// known_hosts complements ssh_config: the declared ones are the user's and
// are never written by Wrekin; the learned ones are the daemon's, recorded
// when a scan finds a checkout or an approved clone succeeds.
package repos

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/plmercereau/wrekin/internal/workspace"
)

// Source says how a learned mapping came to be.
type Source string

const (
	// SourceDiscovered is a checkout found by the discovery scan.
	SourceDiscovered Source = "discovered"
	// SourceCloned is a clone Wrekin made after an approval.
	SourceCloned Source = "cloned"
)

// Entry is one learned mapping.
type Entry struct {
	// Key is the normalised repository (workspace.RepoKey); the file is keyed
	// by it, so it is not repeated inside the entry.
	Key        string    `json:"-"`
	Repository string    `json:"repository"`
	Path       string    `json:"path"`
	Source     Source    `json:"source"`
	AddedAt    time.Time `json:"addedAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
}

type storeFile struct {
	Repositories map[string]Entry `json:"repositories"`
}

// Store reads and rewrites the learned mappings file on every call, so that
// a store opened by the CLI and one held by the daemon never disagree for
// long; writes are atomic (temporary file and rename) and private (0600).
// Concurrent writers in one process are serialised; across processes the
// daemon is the only writer while it runs (the CLI goes through it).
type Store struct {
	path string
	mu   sync.Mutex
}

// New returns the store at path; nothing is read until the first call.
func New(path string) *Store { return &Store{path: path} }

// Path is the file the store reads and writes.
func (s *Store) Path() string { return s.path }

func keyOf(repo string) (string, error) {
	key, ok := workspace.RepoKey(repo)
	if !ok {
		return "", fmt.Errorf("%q is not a repository reference", repo)
	}
	return key, nil
}

func stamp(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// List returns every learned mapping, sorted by key.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	return out, nil
}

// Lookup returns the mapping of repo, in any spelling.
func (s *Store) Lookup(repo string) (Entry, bool, error) {
	key, err := keyOf(repo)
	if err != nil {
		return Entry{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := m[key]
	return e, ok, nil
}

// Record maps repo to path. The same path again only refreshes lastUsedAt
// (the first source and addedAt are kept); another path replaces the entry.
func (s *Store) Record(repo, path string, source Source, now time.Time) error {
	key, err := keyOf(repo)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	now = stamp(now)
	if e, ok := m[key]; ok && e.Path == path {
		e.LastUsedAt = now
		m[key] = e
	} else {
		m[key] = Entry{Key: key, Repository: repo, Path: path, Source: source, AddedAt: now, LastUsedAt: now}
	}
	return s.flush(m)
}

// Touch refreshes lastUsedAt of repo's mapping, if there is one.
func (s *Store) Touch(repo string, now time.Time) error {
	key, err := keyOf(repo)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	e, ok := m[key]
	if !ok {
		return nil
	}
	e.LastUsedAt = stamp(now)
	m[key] = e
	return s.flush(m)
}

// Forget removes repo's mapping and returns it.
func (s *Store) Forget(repo string) (Entry, bool, error) {
	key, err := keyOf(repo)
	if err != nil {
		return Entry{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := m[key]
	if !ok {
		return Entry{}, false, nil
	}
	delete(m, key)
	return e, true, s.flush(m)
}

// ForgetPath removes every mapping to path (a clone being deleted) and
// returns them.
func (s *Store) ForgetPath(path string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	var gone []Entry
	for key, e := range m {
		if e.Path == path {
			gone = append(gone, e)
			delete(m, key)
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	sort.Slice(gone, func(a, b int) bool { return gone[a].Key < gone[b].Key })
	return gone, s.flush(m)
}

// load reads the file (caller holds s.mu). A missing file is empty; a
// corrupt one is an error, so that a write never replaces it blindly.
func (s *Store) load() (map[string]Entry, error) {
	m := map[string]Entry{}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read repository mappings: %w", err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	for key, e := range f.Repositories {
		e.Key = key
		m[key] = e
	}
	return m, nil
}

// flush writes the whole file through a temporary file and a rename (caller
// holds s.mu).
func (s *Store) flush(m map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(storeFile{Repositories: m}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".repositories-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

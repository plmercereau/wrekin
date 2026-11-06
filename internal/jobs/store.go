package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store persists every job in one JSON file, rewritten atomically on each
// change. It is the source of truth: the relay connection is never consulted.
type Store struct {
	path string

	mu   sync.Mutex
	jobs map[string]*Job
}

type storeFile struct {
	Jobs []*Job `json:"jobs"`
}

// Open loads the store at path, creating the parent directory. A missing
// file is an empty store; a corrupt file is an error.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: path, jobs: map[string]*Job{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read job store: %w", err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, j := range f.Jobs {
		s.jobs[j.ID] = j
	}
	return s, nil
}

// Get returns a copy of the job.
func (s *Store) Get(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	return j.clone(), true
}

// FindBySession returns the newest job for a (harness, cloudSessionId) pair.
func (s *Store) FindBySession(harness, cloudSessionID string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Job
	for _, j := range s.jobs {
		if j.Harness != harness || j.CloudSessionID != cloudSessionID {
			continue
		}
		if best == nil || j.CreatedAt.After(best.CreatedAt) || (j.CreatedAt.Equal(best.CreatedAt) && j.ID > best.ID) {
			best = j
		}
	}
	if best == nil {
		return nil, false
	}
	return best.clone(), true
}

// Put stores a copy of the job and rewrites the file.
func (s *Store) Put(j *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j.clone()
	return s.flush()
}

// Delete removes a job and rewrites the file.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
	return s.flush()
}

// List returns copies of all jobs, newest first.
func (s *Store) List() []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedLocked()
}

func (s *Store) sortedLocked() []*Job {
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.clone())
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.After(out[b].CreatedAt)
		}
		return out[a].ID > out[b].ID
	})
	return out
}

// flush writes the whole file through a temporary file and a rename.
func (s *Store) flush() error {
	data, err := json.MarshalIndent(storeFile{Jobs: s.sortedLocked()}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".jobs-*.tmp")
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

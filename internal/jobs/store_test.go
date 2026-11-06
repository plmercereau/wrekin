package jobs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/protocol"
)

func TestStoreRoundTripKeepsLocalFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "jobs.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	approved := time.Date(2026, 11, 5, 22, 0, 0, 0, time.UTC)
	job := &Job{
		Job: protocol.Job{
			ID: "job_1", Harness: "claude-code", CloudSessionID: "session_1", State: protocol.StateReady,
			Repository: &protocol.Repository{Input: "o/r", URL: "https://github.com/o/r", Name: "r"},
			Workdir:    "/w", Session: &protocol.Session{Terminal: "tmux", Name: "wrekin-1", Attach: "wrekin attach wrekin-1"},
			Ready:     &protocol.Ready{Confirmed: true, Detail: "prompt visible"},
			CreatedAt: approved, UpdatedAt: approved,
		},
		CreatedClone:    true,
		CreatedWorktree: true,
		CheckoutDir:     "/c",
		ClonePath:       "/c",
		AttentionReason: "clone",
		ApprovedAt:      &approved,
		Params:          protocol.TeleportParams{Harness: "claude-code", CloudSessionID: "session_1", Repository: "o/r"},
	}
	if err := store.Put(job); err != nil {
		t.Fatalf("Put: %v", err)
	}
	older := &Job{Job: protocol.Job{ID: "job_0", Harness: "claude-code", CloudSessionID: "session_0", State: protocol.StateFailed,
		CreatedAt: approved.Add(-time.Hour), UpdatedAt: approved.Add(-time.Hour)}}
	if err := store.Put(older); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("jobs file mode %o, want 0600", info.Mode().Perm())
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := again.Get("job_1")
	if !ok {
		t.Fatal("job_1 missing after reopen")
	}
	if !reflect.DeepEqual(got, job) {
		t.Errorf("round trip differs:\n got %+v\nwant %+v", got, job)
	}
	list := again.List()
	if len(list) != 2 || list[0].ID != "job_1" || list[1].ID != "job_0" {
		t.Errorf("List order wrong: %v %v", list[0].ID, list[1].ID)
	}
	found, ok := again.FindBySession("claude-code", "session_1")
	if !ok || found.ID != "job_1" {
		t.Errorf("FindBySession = %v %v", found, ok)
	}
	if _, ok := again.FindBySession("claude-code", "nope"); ok {
		t.Error("FindBySession found a job that does not exist")
	}
	// The wire view has no local fields.
	wire := got.Wire()
	if wire.ID != "job_1" || wire.Workdir != "/w" {
		t.Errorf("Wire = %+v", wire)
	}
	// Mutating a returned job does not change the store until Put.
	got.State = protocol.StateFailed
	if again2, _ := again.Get("job_1"); again2.State != protocol.StateReady {
		t.Error("Get must return a copy")
	}
	if err := again.Delete("job_0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get("job_0"); ok {
		t.Error("job_0 still present after Delete")
	}
}

func TestOpenMissingDirectoryAndCorruptFile(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a", "b", "jobs.json"))
	if err != nil {
		t.Fatalf("Open with missing parents: %v", err)
	}
	if len(s.List()) != 0 {
		t.Error("new store is not empty")
	}
	bad := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bad); err == nil {
		t.Error("a corrupt store must be reported, not silently emptied")
	}
}

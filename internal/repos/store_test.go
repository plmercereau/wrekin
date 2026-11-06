package repos

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTripAndPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "repositories.json")
	s := New(path)
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("missing file: %v %v, want an empty list", list, err)
	}
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 123, time.FixedZone("x", 3600))
	if err := s.Record("git@github.com:o/n.git", "/p/n", SourceDiscovered, t0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Record("https://gitlab.com/g/r", "/p/r", SourceCloned, t0); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %04o, want 0600", info.Mode().Perm())
	}

	// A second store over the same file sees the entries, keyed by the
	// normalised form whatever spelling is used.
	e, ok, err := New(path).Lookup("https://github.com/o/n")
	if err != nil || !ok {
		t.Fatalf("Lookup: %v %v", ok, err)
	}
	want := Entry{Key: "github.com/o/n", Repository: "git@github.com:o/n.git", Path: "/p/n", Source: SourceDiscovered, AddedAt: t0.UTC().Truncate(time.Second), LastUsedAt: t0.UTC().Truncate(time.Second)}
	if e != want {
		t.Errorf("entry = %+v\nwant    %+v", e, want)
	}

	// The file is keyed by the normalised form and holds RFC 3339 times.
	var raw struct {
		Repositories map[string]map[string]string `json:"repositories"`
	}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("file is not the expected JSON: %v\n%s", err, data)
	}
	got := raw.Repositories["gitlab.com/g/r"]
	if got["path"] != "/p/r" || got["source"] != "cloned" || got["addedAt"] != "2026-11-05T09:00:00Z" || got["lastUsedAt"] != "2026-11-05T09:00:00Z" {
		t.Errorf("gitlab entry on disk = %v", got)
	}

	// Recording the same path again only refreshes lastUsedAt; Touch too.
	t1 := t0.Add(time.Hour)
	if err := s.Record("o/n", "/p/n", SourceCloned, t1); err != nil {
		t.Fatal(err)
	}
	e, _, _ = s.Lookup("o/n")
	if e.Source != SourceDiscovered || !e.AddedAt.Equal(want.AddedAt) || !e.LastUsedAt.Equal(t1.UTC().Truncate(time.Second)) {
		t.Errorf("after re-record: %+v", e)
	}
	t2 := t1.Add(time.Hour)
	if err := s.Touch("o/n", t2); err != nil {
		t.Fatal(err)
	}
	e, _, _ = s.Lookup("o/n")
	if !e.LastUsedAt.Equal(t2.UTC().Truncate(time.Second)) {
		t.Errorf("after touch: %+v", e)
	}
	// Recording another path replaces the entry.
	if err := s.Record("o/n", "/elsewhere/n", SourceCloned, t2); err != nil {
		t.Fatal(err)
	}
	e, _, _ = s.Lookup("o/n")
	if e.Path != "/elsewhere/n" || e.Source != SourceCloned || !e.AddedAt.Equal(t2.UTC().Truncate(time.Second)) {
		t.Errorf("after move: %+v", e)
	}

	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].Key != "github.com/o/n" || list[1].Key != "gitlab.com/g/r" {
		t.Errorf("List = %+v, %v; want both entries sorted by key", list, err)
	}

	removed, ok, err := s.Forget("git@github.com:o/n")
	if err != nil || !ok || removed.Path != "/elsewhere/n" {
		t.Errorf("Forget = %+v %v %v", removed, ok, err)
	}
	if _, ok, _ := s.Forget("o/n"); ok {
		t.Error("forgetting twice must report nothing removed")
	}
	gone, err := s.ForgetPath("/p/r")
	if err != nil || len(gone) != 1 || gone[0].Key != "gitlab.com/g/r" {
		t.Errorf("ForgetPath = %+v %v", gone, err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("store should be empty, has %+v", list)
	}
	if err := s.Record("not a repository", "/x", SourceCloned, t0); err == nil {
		t.Error("Record must refuse an unparsable repository")
	}
}

func TestStoreCorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repositories.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(path)
	if _, err := s.List(); err == nil {
		t.Error("List must report a corrupt file")
	}
	if err := s.Record("o/n", "/x", SourceCloned, time.Now()); err == nil {
		t.Error("Record must not overwrite a corrupt file")
	}
}

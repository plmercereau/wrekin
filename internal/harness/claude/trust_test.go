package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

const sampleClaudeJSON = `{
  "numStartups": 3,
  "theme": "dark",
  "projects": {
    "/home/me/a": {
      "allowedTools": ["Bash(ls:*)"],
      "hasTrustDialogAccepted": true,
      "history": [{"display": "hi", "pastedContents": {}}]
    },
    "/home/me/b": {
      "allowedTools": [],
      "hasTrustDialogAccepted": false
    }
  },
  "oauthAccount": {"emailAddress": "me@example.com"}
}
`

func writeSample(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(sampleClaudeJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTrustedReadsClaudeJSON(t *testing.T) {
	c := New(writeSample(t), "claude")
	cases := map[string]bool{"/home/me/a": true, "/home/me/b": false, "/home/me/unknown": false}
	for dir, want := range cases {
		got, err := c.Trusted(dir)
		if err != nil {
			t.Fatalf("Trusted(%s): %v", dir, err)
		}
		if got != want {
			t.Errorf("Trusted(%s) = %v, want %v", dir, got, want)
		}
	}
	missing := New(filepath.Join(t.TempDir(), "absent.json"), "claude")
	if got, err := missing.Trusted("/x"); err != nil || got {
		t.Errorf("a missing file trusts nothing: %v %v", got, err)
	}
}

func TestTrustWritesKeyAndBackup(t *testing.T) {
	path := writeSample(t)
	c := New(path, "claude")

	if err := c.Trust("/home/me/b"); err != nil {
		t.Fatalf("Trust(b): %v", err)
	}
	if err := c.Trust("/home/me/new"); err != nil {
		t.Fatalf("Trust(new): %v", err)
	}

	backup, err := os.ReadFile(path + ".wrekin-backup")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	// The backup is taken before each write; after two writes it holds the
	// state before the second one, which must still parse and keep /home/me/a.
	var backupDoc map[string]any
	if err := json.Unmarshal(backup, &backupDoc); err != nil {
		t.Fatalf("backup is not JSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	var orig map[string]any
	_ = json.Unmarshal([]byte(sampleClaudeJSON), &orig)

	for _, key := range []string{"numStartups", "theme", "oauthAccount"} {
		if !reflect.DeepEqual(got[key], orig[key]) {
			t.Errorf("key %q changed: %v -> %v", key, orig[key], got[key])
		}
	}
	projects := got["projects"].(map[string]any)
	if !reflect.DeepEqual(projects["/home/me/a"], orig["projects"].(map[string]any)["/home/me/a"]) {
		t.Errorf("untouched project changed: %v", projects["/home/me/a"])
	}
	b := projects["/home/me/b"].(map[string]any)
	if b["hasTrustDialogAccepted"] != true || !reflect.DeepEqual(b["allowedTools"], []any{}) {
		t.Errorf("project b = %v", b)
	}
	n := projects["/home/me/new"].(map[string]any)
	if n["hasTrustDialogAccepted"] != true {
		t.Errorf("new project = %v", n)
	}
	for _, dir := range []string{"/home/me/a", "/home/me/b", "/home/me/new"} {
		if ok, _ := c.Trusted(dir); !ok {
			t.Errorf("Trusted(%s) false after Trust", dir)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode changed to %o", info.Mode().Perm())
	}
}

func TestTrustRefusesUnrecognisedFile(t *testing.T) {
	for _, body := range []string{`[]`, `{"projects": []}`, `{"projects": null}`, `not json`, `"str"`} {
		path := filepath.Join(t.TempDir(), ".claude.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		c := New(path, "claude")
		err := c.Trust("/x")
		if !errors.Is(err, ErrUnrecognisedClaudeJSON) {
			t.Errorf("%s: err = %v, want ErrUnrecognisedClaudeJSON", body, err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != body {
			t.Errorf("%s: file was modified", body)
		}
		if _, err := os.Stat(path + ".wrekin-backup"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: a backup was written although the file was refused", body)
		}
		if _, err := c.Trusted("/x"); !errors.Is(err, ErrUnrecognisedClaudeJSON) && body != `not json` && body != `"str"` {
			t.Errorf("%s: Trusted err = %v", body, err)
		}
	}
}

// On a machine where Claude Code has never run, there is no file: Trust
// creates it, private, with the one entry, and writes no backup of nothing.
func TestTrustCreatesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", ".claude.json")
	c := New(path, "claude")
	if ok, err := c.Trusted("/x"); ok || err != nil {
		t.Fatalf("Trusted on a missing file = %v, %v", ok, err)
	}
	if err := c.Trust("/x"); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	var got map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"projects": map[string]any{"/x": map[string]any{"hasTrustDialogAccepted": true}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file = %v, want %v", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".wrekin-backup"); !errors.Is(err, os.ErrNotExist) {
		t.Error("a backup was written for a file that did not exist")
	}
	if ok, err := c.Trusted("/x"); !ok || err != nil {
		t.Errorf("Trusted after Trust = %v, %v", ok, err)
	}
}

// What `claude auth status` leaves on a fresh machine: no "projects" yet.
// Trust adds it and keeps every other key.
func TestTrustAddsProjectsToAFileWithout(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	fresh := `{"firstStartTime": "2026-11-06T15:40:45.396Z", "migrationVersion": 13, "userID": "abc"}`
	if err := os.WriteFile(path, []byte(fresh), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(path, "claude")
	if ok, err := c.Trusted("/x"); ok || err != nil {
		t.Fatalf("Trusted = %v, %v", ok, err)
	}
	if err := c.Trust("/x"); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	var got map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["userID"] != "abc" || got["migrationVersion"] != float64(13) || got["firstStartTime"] == nil {
		t.Errorf("other keys lost: %v", got)
	}
	if ok, _ := c.Trusted("/x"); !ok {
		t.Error("not trusted after Trust")
	}
	backup, _ := os.ReadFile(path + ".wrekin-backup")
	if string(backup) != fresh {
		t.Errorf("backup = %q", backup)
	}
}

func TestDefaultClaudeJSONPathFollowsClaudeConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/srv/claude")
	if got := DefaultClaudeJSONPath(); got != "/srv/claude/.claude.json" {
		t.Errorf("path = %q", got)
	}
}

func TestCompleteOnboardingSetsTheKeyOnceAndKeepsTheRest(t *testing.T) {
	path := writeSample(t)
	c := New(path, "claude")
	changed, err := c.CompleteOnboarding()
	if err != nil || !changed {
		t.Fatalf("CompleteOnboarding = %v, %v", changed, err)
	}
	var got map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["hasCompletedOnboarding"] != true || got["theme"] != "dark" || got["projects"] == nil {
		t.Errorf("file = %v", got)
	}
	if backup, _ := os.ReadFile(path + ".wrekin-backup"); string(backup) != sampleClaudeJSON {
		t.Error("no backup of the previous file")
	}
	if changed, err := c.CompleteOnboarding(); err != nil || changed {
		t.Errorf("second call = %v, %v, want no change", changed, err)
	}

	missing := New(filepath.Join(t.TempDir(), ".claude.json"), "claude")
	if changed, err := missing.CompleteOnboarding(); err != nil || !changed {
		t.Fatalf("on a missing file = %v, %v", changed, err)
	}
	if ok, err := missing.Trusted("/x"); ok || err != nil {
		t.Errorf("the created file must stay readable: %v, %v", ok, err)
	}
}

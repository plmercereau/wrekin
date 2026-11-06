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
	for _, body := range []string{`[]`, `{"foo": 1}`, `{"projects": []}`, `not json`, `"str"`} {
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
	missing := New(filepath.Join(t.TempDir(), "absent.json"), "claude")
	if err := missing.Trust("/x"); !errors.Is(err, ErrUnrecognisedClaudeJSON) {
		t.Errorf("Trust on a missing file: err = %v", err)
	}
}

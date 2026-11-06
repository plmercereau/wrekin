package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrUnrecognisedClaudeJSON means ~/.claude.json is not a JSON object, or has
// a "projects" value that is not an object; Wrekin then refuses to touch it.
var ErrUnrecognisedClaudeJSON = errors.New("~/.claude.json does not have the expected shape")

// trustKey is the per-project flag Claude Code sets once the user has
// accepted the trust dialog for a directory. This is a version-sensitive
// integration (feasibility test P3), not a documented interface.
const trustKey = "hasTrustDialogAccepted"

// claudeJSON is the minimal parsed view: every other key is kept verbatim.
type claudeJSON struct {
	top      map[string]json.RawMessage
	projects map[string]json.RawMessage
}

// read parses the file. A file without "projects" is a valid one: Claude Code
// writes it so on a machine where no project has been opened yet (a first
// `claude auth login` or `claude auth status` does), and the key is added on
// the first Trust.
func (c *Claude) read() (*claudeJSON, error) {
	data, err := os.ReadFile(c.claudeJSON)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return nil, fmt.Errorf("%w: top level is not an object", ErrUnrecognisedClaudeJSON)
	}
	projects := map[string]json.RawMessage{}
	if rawProjects, ok := top["projects"]; ok {
		projects = nil
		if err := json.Unmarshal(rawProjects, &projects); err != nil || projects == nil {
			return nil, fmt.Errorf("%w: \"projects\" is not an object", ErrUnrecognisedClaudeJSON)
		}
	}
	return &claudeJSON{top: top, projects: projects}, nil
}

// Trusted implements harness.Harness. A missing file trusts nothing.
func (c *Claude) Trusted(dir string) (bool, error) {
	doc, err := c.read()
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, ok := doc.projects[dir]
	if !ok {
		return false, nil
	}
	var project map[string]json.RawMessage
	if err := json.Unmarshal(raw, &project); err != nil {
		return false, nil
	}
	var accepted bool
	if err := json.Unmarshal(project[trustKey], &accepted); err != nil {
		return false, nil
	}
	return accepted, nil
}

// Trust implements harness.Harness: it sets projects[dir].hasTrustDialogAccepted
// to true, after copying the file to <file>.wrekin-backup. Every other value
// is preserved. On a machine where Claude Code has never run, the file does
// not exist: it is created (mode 0600) with that one entry, and Claude Code
// adds the rest of its configuration on its first start.
func (c *Claude) Trust(dir string) error {
	doc, err := c.read()
	existed := true
	if errors.Is(err, fs.ErrNotExist) {
		existed = false
		doc, err = &claudeJSON{top: map[string]json.RawMessage{}, projects: map[string]json.RawMessage{}}, nil
	}
	if err != nil {
		return err
	}
	project := map[string]json.RawMessage{}
	if raw, ok := doc.projects[dir]; ok {
		if err := json.Unmarshal(raw, &project); err != nil || project == nil {
			project = map[string]json.RawMessage{}
		}
	}
	project[trustKey] = json.RawMessage("true")
	rawProject, err := json.Marshal(project)
	if err != nil {
		return err
	}
	doc.projects[dir] = rawProject
	rawProjects, err := json.Marshal(doc.projects)
	if err != nil {
		return err
	}
	doc.top["projects"] = rawProjects
	return c.write(doc, existed)
}

// onboardingKey is set by Claude Code once its first-run screens (the theme
// choice among them) are done; until then every interactive start shows
// them, a teleport in a detached terminal included.
const onboardingKey = "hasCompletedOnboarding"

// CompleteOnboarding sets hasCompletedOnboarding to true, so that Claude Code
// opens a teleported session instead of its theme picker, which no one
// answers on a node until the user attaches. It reports whether it changed
// anything; a missing file is created as Trust does, and the same backup is
// written first.
func (c *Claude) CompleteOnboarding() (bool, error) {
	doc, err := c.read()
	existed := true
	if errors.Is(err, fs.ErrNotExist) {
		existed = false
		doc, err = &claudeJSON{top: map[string]json.RawMessage{}, projects: map[string]json.RawMessage{}}, nil
	}
	if err != nil {
		return false, err
	}
	var done bool
	if raw, ok := doc.top[onboardingKey]; ok && json.Unmarshal(raw, &done) == nil && done {
		return false, nil
	}
	doc.top[onboardingKey] = json.RawMessage("true")
	return true, c.write(doc, existed)
}

// write replaces the file with doc, after a backup of the file it replaces;
// a file that did not exist is created, with its directory, private.
func (c *Claude) write(doc *claudeJSON, existed bool) error {
	out, err := json.MarshalIndent(doc.top, "", "  ")
	if err != nil {
		return err
	}
	if existed {
		if err := c.backup(); err != nil {
			return fmt.Errorf("backup %s: %w", c.claudeJSON, err)
		}
	} else if err := os.MkdirAll(filepath.Dir(c.claudeJSON), 0o700); err != nil {
		return err
	}
	return writeAtomic(c.claudeJSON, append(out, '\n'))
}

func (c *Claude) backup() error {
	data, err := os.ReadFile(c.claudeJSON)
	if err != nil {
		return err
	}
	info, err := os.Stat(c.claudeJSON)
	if err != nil {
		return err
	}
	return os.WriteFile(c.claudeJSON+".wrekin-backup", data, info.Mode().Perm())
}

// writeAtomic replaces path through a temporary file, keeping its mode.
func writeAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

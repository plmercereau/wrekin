package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/paths"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	a := &app{version: "1.2.3-test", stdout: &out, stderr: &errOut, stdin: strings.NewReader("")}
	root := newRootCommand(a)
	root.SetArgs(args)
	err := root.Execute()
	return out.String() + errOut.String(), err
}

func TestCLIStatusWithoutDaemonExplains(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out, err := run(t, "status")
	if !errors.Is(err, errSilent) {
		t.Fatalf("err = %v, want errSilent (exit code 1 without a second message)", err)
	}
	for _, want := range []string{"not running", "wrekin auth", "wrekin start"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	id, _ := identity.Generate("box")
	id.NodeID = "node_01J"
	if err := id.Save(paths.IdentityFile()); err != nil {
		t.Fatal(err)
	}
	out, _ = run(t, "status")
	if !strings.Contains(out, "node_01J") || !strings.Contains(out, "wrekin start") || strings.Contains(out, "wrekin auth") {
		t.Errorf("paired output wrong:\n%s", out)
	}
	out, err = run(t, "sessions")
	if !errors.Is(err, errSilent) || !strings.Contains(out, "not running") {
		t.Errorf("sessions without daemon: %v\n%s", err, out)
	}
}

func TestCLIVersionAndSettingsFlag(t *testing.T) {
	out, err := run(t, "--version")
	if err != nil || !strings.Contains(out, "1.2.3-test") {
		t.Errorf("--version: %v\n%s", err, out)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, err = run(t, "logout", "--settings", filepath.Join(t.TempDir(), "settings.json"))
	if err != nil || !strings.Contains(out, "not paired") {
		t.Errorf("logout when unpaired: %v\n%s", err, out)
	}
	if _, err := run(t, "attach", "../evil"); err == nil {
		t.Error("attach must refuse a hostile session name")
	}
}

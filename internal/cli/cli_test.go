package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

// fakeAuth approves the device flow at once, in process; the pairing package
// tests the flow itself.
type fakeAuth struct{}

func (fakeAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	switch req.URL.Path {
	case "/api/auth/device/code":
		fmt.Fprint(rec, `{"device_code":"d","user_code":"U","verification_uri_complete":"https://wrekin.test/device?user_code=U","expires_in":60,"interval":1}`)
	case "/api/auth/device/token":
		fmt.Fprint(rec, `{"access_token":"tok","token_type":"Bearer"}`)
	case "/api/nodes":
		rec.WriteHeader(http.StatusCreated)
		fmt.Fprint(rec, `{"nodeId":"node_01J","accountId":"usr_1"}`)
	default:
		fmt.Fprint(rec, `{}`)
	}
	return rec.Result(), nil
}

// `wrekin auth` writes identity.json and, when absent, settings.json; both
// directories it creates must be private, whichever is created first.
func TestAuthCreatesPrivateConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	settingsPath := filepath.Join(t.TempDir(), "fresh", "settings.json")
	var out bytes.Buffer
	a := &app{version: "t", stdout: &out, stderr: &out, stdin: strings.NewReader(""), httpClient: &http.Client{Transport: fakeAuth{}}}
	root := newRootCommand(a)
	root.SetArgs([]string{"auth", "--settings", settingsPath, "--relay", "wss://wrekin.test/connect"})
	if err := root.Execute(); err != nil {
		t.Fatalf("auth: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Paired as node node_01J") {
		t.Errorf("output:\n%s", out.String())
	}
	for _, dir := range []string{paths.ConfigDir(), filepath.Dir(settingsPath)} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %04o, want 0700", dir, info.Mode().Perm())
		}
	}
	info, err := os.Stat(paths.IdentityFile())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("identity.json: %v mode %v", err, info)
	}
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("settings.json not written: %v", err)
	}
}

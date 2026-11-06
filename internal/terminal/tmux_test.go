package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func newTestTmux(t *testing.T) *Tmux {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary not installed")
	}
	// A private server (-L) in a private socket directory: the tests never
	// touch the user's tmux server.
	t.Setenv("TMUX_TMPDIR", os.TempDir())
	tm := NewTmux()
	tm.SocketName = fmt.Sprintf("wrekin-test-%d", os.Getpid())
	t.Cleanup(func() { _ = tm.killServer() })
	// Probe once: a sandbox that forbids unix sockets makes tmux print
	// "Operation not permitted" and exit 0, without any server.
	probe := fmt.Sprintf("wrekin-probe-%d", time.Now().UnixNano()%1e6)
	if err := tm.Launch(context.Background(), probe, os.TempDir(), []string{"sleep", "5"}); err != nil {
		if strings.Contains(err.Error(), "Operation not permitted") {
			t.Skipf("tmux cannot create its server socket here (sandboxed?): %v", err)
		}
		t.Fatalf("tmux probe launch: %v", err)
	}
	_ = tm.Kill(probe)
	return tm
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTmuxLaunchExistsCaptureKill(t *testing.T) {
	tm := newTestTmux(t)
	if err := tm.Available(); err != nil {
		t.Fatalf("Available: %v", err)
	}
	if tm.Name() != "tmux" {
		t.Errorf("Name = %q", tm.Name())
	}
	session := fmt.Sprintf("wrekin-t%d", time.Now().UnixNano()%1e6)
	workdir := t.TempDir()

	exists, err := tm.Exists(session)
	if err != nil || exists {
		t.Fatalf("session must not exist yet: %v %v", exists, err)
	}
	// `exec` so that the pane's foreground process becomes `sleep`, the way a
	// harness takes the pane over once its shell wrapper has started it: tmux's
	// pane_current_command names the foreground process group leader, which
	// stays `sh` as long as the shell is only waiting on a child.
	if err := tm.Launch(context.Background(), session, workdir, []string{"sh", "-c", "echo READY; exec sleep 30"}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() { _ = tm.Kill(session) })

	waitFor(t, "session to exist", func() bool { ok, _ := tm.Exists(session); return ok })
	waitFor(t, "READY in the pane", func() bool {
		out, err := tm.Capture(session, 40)
		return err == nil && strings.Contains(out, "READY")
	})
	running, err := tm.Running(session)
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if !running {
		t.Error("sleep is running in the pane, Running must be true")
	}
	names, err := tm.List("wrekin-t")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 1 || names[0] != session {
		t.Errorf("List = %v, want [%s]", names, session)
	}
	if got := tm.AttachCommand(session); got[0] != "tmux" || got[len(got)-1] != "="+session {
		t.Errorf("AttachCommand = %v", got)
	}
	if err := tm.Launch(context.Background(), session, workdir, []string{"sh"}); err == nil {
		t.Error("launching a duplicate session must fail")
	}

	if err := tm.Kill(session); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitFor(t, "session to disappear", func() bool { ok, _ := tm.Exists(session); return !ok })
}

func TestTmuxRunningIsFalseWhenCommandExited(t *testing.T) {
	tm := newTestTmux(t)
	session := fmt.Sprintf("wrekin-x%d", time.Now().UnixNano()%1e6)
	// The shell stays alive after the command exits only because we exec a
	// shell explicitly; the pane then shows a shell prompt, not the harness.
	if err := tm.Launch(context.Background(), session, t.TempDir(), []string{"sh", "-c", "echo DONE; exec sh"}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() { _ = tm.Kill(session) })
	waitFor(t, "shell prompt", func() bool {
		running, err := tm.Running(session)
		return err == nil && !running
	})
}

func TestTmuxQuotesArguments(t *testing.T) {
	got := shellJoin([]string{"claude", "--teleport", "session_01 CN", "it's"})
	want := `'claude' '--teleport' 'session_01 CN' 'it'\''s'`
	if got != want {
		t.Errorf("shellJoin = %s, want %s", got, want)
	}
}

func TestLaunchArgsKeepDetachedSessionAlive(t *testing.T) {
	got := strings.Join(launchArgs("wrekin-abc", "/w", []string{"claude", "--teleport", "s"}), " ")
	want := "new-session -d -s wrekin-abc -c /w 'claude' '--teleport' 's' ; set-option -t =wrekin-abc destroy-unattached off"
	if got != want {
		t.Errorf("launchArgs =\n %s\nwant\n %s", got, want)
	}
}

func TestLookup(t *testing.T) {
	if _, err := Lookup("screen"); err == nil {
		t.Error("screen is not supported")
	}
	term, err := Lookup("tmux")
	if err != nil {
		t.Fatalf("Lookup(tmux): %v", err)
	}
	if term.Name() != "tmux" {
		t.Errorf("Name = %q", term.Name())
	}
}

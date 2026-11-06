package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/jobs"
	"github.com/plmercereau/wrekin/internal/protocol"
)

type fakeRunner struct {
	teleports []protocol.TeleportParams
	statuses  []string
	approvals []protocol.ApproveParams
	fail      *protocol.JobError
}

func (f *fakeRunner) job(id string) *jobs.Job {
	return &jobs.Job{Job: protocol.Job{ID: id, Harness: "claude-code", CloudSessionID: "s", State: protocol.StateAccepted}, CreatedClone: true}
}

func (f *fakeRunner) Teleport(ctx context.Context, p protocol.TeleportParams) (*jobs.Job, *protocol.JobError) {
	f.teleports = append(f.teleports, p)
	if f.fail != nil {
		return nil, f.fail
	}
	return f.job("job_t"), nil
}

func (f *fakeRunner) Status(id string) (*jobs.Job, *protocol.JobError) {
	f.statuses = append(f.statuses, id)
	if id == "job_missing" {
		return nil, &protocol.JobError{Code: protocol.CodeUnknownJob, Message: "no job of that id"}
	}
	return f.job(id), nil
}

func (f *fakeRunner) Approve(id string, approved bool) (*jobs.Job, *protocol.JobError) {
	f.approvals = append(f.approvals, protocol.ApproveParams{JobID: id, Approved: approved})
	return f.job(id), nil
}

func (f *fakeRunner) List() ([]*jobs.Job, *protocol.JobError) {
	return []*jobs.Job{f.job("job_1"), f.job("job_2")}, nil
}

func command(t *testing.T, id, name string, params any) protocol.Command {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Command{Envelope: protocol.Envelope{V: 1, Type: "command"}, ID: id, Name: name, Params: raw}
}

func roundTrip(t *testing.T, res protocol.Result) map[string]any {
	t.Helper()
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDaemonHandlerMapsCommands(t *testing.T) {
	fr := &fakeRunner{}
	h := NewHandler(fr, func() time.Time { return time.Date(2026, 11, 6, 1, 2, 3, 0, time.UTC) })
	ctx := context.Background()

	res := h.Handle(ctx, command(t, "c1", "teleport", protocol.TeleportParams{Harness: "claude-code", CloudSessionID: "session_1", Repository: "o/r"}))
	m := roundTrip(t, res)
	if m["v"] != float64(1) || m["type"] != "result" || m["id"] != "c1" || m["ok"] != true {
		t.Errorf("teleport result header: %v", m)
	}
	if len(fr.teleports) != 1 || fr.teleports[0].Repository != "o/r" {
		t.Errorf("teleport params not passed: %+v", fr.teleports)
	}
	data := m["data"].(map[string]any)
	if data["id"] != "job_t" || data["state"] != "accepted" {
		t.Errorf("teleport data = %v", data)
	}
	if _, leaked := data["createdClone"]; leaked {
		t.Error("local-only fields must not reach the relay")
	}

	res = h.Handle(ctx, command(t, "c2", "get_teleport_status", protocol.StatusParams{JobID: "job_9"}))
	if !res.OK || fr.statuses[0] != "job_9" {
		t.Errorf("status result = %+v statuses=%v", res, fr.statuses)
	}
	res = h.Handle(ctx, command(t, "c3", "get_teleport_status", protocol.StatusParams{JobID: "job_missing"}))
	if res.OK || res.Error == nil || res.Error.Code != protocol.CodeUnknownJob {
		t.Errorf("unknown job result = %+v", res)
	}
	res = h.Handle(ctx, command(t, "c4", "get_teleport_status", map[string]any{}))
	if res.OK || res.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("missing jobId result = %+v", res)
	}

	res = h.Handle(ctx, command(t, "c5", "approve_repository", protocol.ApproveParams{JobID: "job_a", Approved: true}))
	if !res.OK || len(fr.approvals) != 1 || !fr.approvals[0].Approved || fr.approvals[0].JobID != "job_a" {
		t.Errorf("approve result = %+v approvals=%v", res, fr.approvals)
	}

	res = h.Handle(ctx, command(t, "c6", "list_jobs", map[string]any{}))
	m = roundTrip(t, res)
	list := m["data"].(map[string]any)["jobs"].([]any)
	if len(list) != 2 {
		t.Errorf("list_jobs = %v", m["data"])
	}

	res = h.Handle(ctx, command(t, "c7", "ping", map[string]any{}))
	m = roundTrip(t, res)
	if m["data"].(map[string]any)["at"] != "2026-11-06T01:02:03Z" {
		t.Errorf("ping = %v", m["data"])
	}

	res = h.Handle(ctx, command(t, "c8", "format_disk", map[string]any{}))
	if res.OK || res.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("unknown command result = %+v", res)
	}
	res = h.Handle(ctx, protocol.Command{ID: "c9", Name: "teleport", Params: json.RawMessage(`{"harness": 42}`)})
	if res.OK || res.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("malformed params result = %+v", res)
	}

	fr.fail = &protocol.JobError{Code: protocol.CodeConcurrencyLimit, Message: "busy"}
	res = h.Handle(ctx, command(t, "c10", "teleport", protocol.TeleportParams{Harness: "claude-code", CloudSessionID: "s"}))
	if res.OK || res.Error.Code != protocol.CodeConcurrencyLimit || res.Error.Message != "busy" {
		t.Errorf("runner error result = %+v", res)
	}
}

func TestRotatingLogWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrekin.log")
	w, err := newRotatingWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n"
	for i := 0; i < 10; i++ { // 400 bytes: enough for several rotations
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, want := range []string{"wrekin.log", "wrekin.log.1", "wrekin.log.2", "wrekin.log.3"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), want)); err != nil {
			t.Errorf("%s missing (have %v)", want, names)
		}
	}
	if _, err := os.Stat(path + ".4"); err == nil {
		t.Error("more than 3 rotated files kept")
	}
	info, _ := os.Stat(path)
	if info.Size() > 100 {
		t.Errorf("active log is %d bytes, over the limit", info.Size())
	}
}

func TestRunWithoutIdentityExplains(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Run(ctx, Options{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Version: "test"})
	if err == nil || !strings.Contains(err.Error(), "wrekin auth") {
		t.Fatalf("err = %v, want a hint to run wrekin auth", err)
	}
}

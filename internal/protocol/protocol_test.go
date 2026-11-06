package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDecodeCommandTeleport(t *testing.T) {
	raw := `{ "v": 1, "type": "command", "id": "cmd_1", "name": "teleport",
	  "params": { "harness": "claude-code", "cloudSessionId": "session_01CN", "repository": "plmercereau/wrekin", "relaunch": true } }`
	msg, err := Decode([]byte(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	cmd, ok := msg.(*Command)
	if !ok {
		t.Fatalf("decoded %T, want *Command", msg)
	}
	if cmd.ID != "cmd_1" || cmd.Name != "teleport" || cmd.V != Version {
		t.Errorf("command header = %+v", cmd)
	}
	var p TeleportParams
	if err := cmd.DecodeParams(&p); err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	want := TeleportParams{Harness: "claude-code", CloudSessionID: "session_01CN", Repository: "plmercereau/wrekin", Relaunch: true}
	if p != want {
		t.Errorf("params = %+v, want %+v", p, want)
	}
}

func TestDecodeHandshakeMessages(t *testing.T) {
	cases := map[string]any{
		`{"v":1,"type":"challenge","nonce":"abc"}`:                                        &Challenge{},
		`{"v":1,"type":"welcome","accountId":"usr_1","nodeId":"node_1","pingSeconds":30}`: &Welcome{},
		`{"v":1,"type":"pong"}`: &Pong{},
		`{"v":1,"type":"ping"}`: &Ping{},
		`{"v":1,"type":"error","code":"unknown_node","message":"no such node"}`: &ErrorMessage{},
		`{"v":1,"type":"result","id":"cmd_1","ok":true,"data":{"at":"x"}}`:      &Result{},
	}
	for raw, want := range cases {
		got, err := Decode([]byte(raw))
		if err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		if reflect.TypeOf(got) != reflect.TypeOf(want) {
			t.Errorf("%s: decoded %T, want %T", raw, got, want)
		}
	}
	w, _ := Decode([]byte(`{"v":1,"type":"welcome","accountId":"usr_1","nodeId":"node_1","pingSeconds":30}`))
	if w.(*Welcome).PingSeconds != 30 || w.(*Welcome).AccountID != "usr_1" {
		t.Errorf("welcome fields not decoded: %+v", w)
	}
}

func TestUnknownTypeIsIgnored(t *testing.T) {
	msg, err := Decode([]byte(`{"v":1,"type":"something-new","payload":{"x":1}}`))
	if err != nil {
		t.Fatalf("unknown type must not be an error: %v", err)
	}
	u, ok := msg.(*UnknownMessage)
	if !ok || u.Type != "something-new" {
		t.Fatalf("decoded %T %+v, want *UnknownMessage", msg, msg)
	}
	if _, err := Decode([]byte(`{"v":2,"type":"ping"}`)); err == nil {
		t.Error("a different protocol version must be refused")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Error("malformed JSON must be an error")
	}
}

func TestJobJSONMatchesProtocolExample(t *testing.T) {
	example := `{
  "id": "job_01J",
  "harness": "claude-code",
  "cloudSessionId": "session_01CN",
  "state": "ready",
  "repository": { "input": "plmercereau/wrekin", "url": "https://github.com/plmercereau/wrekin", "name": "wrekin" },
  "workdir": "/home/me/projects/wrekin/.claude/worktrees/wrekin-01CN",
  "session": { "terminal": "tmux", "name": "wrekin-01CN", "attach": "wrekin attach wrekin-01CN" },
  "attention": null,
  "ready": { "confirmed": true, "detail": "prompt visible" },
  "error": null,
  "createdAt": "2026-11-05T22:10:03Z",
  "updatedAt": "2026-11-05T22:10:41Z"
}`
	job := Job{
		ID:             "job_01J",
		Harness:        "claude-code",
		CloudSessionID: "session_01CN",
		State:          StateReady,
		Repository:     &Repository{Input: "plmercereau/wrekin", URL: "https://github.com/plmercereau/wrekin", Name: "wrekin"},
		Workdir:        "/home/me/projects/wrekin/.claude/worktrees/wrekin-01CN",
		Session:        &Session{Terminal: "tmux", Name: "wrekin-01CN", Attach: "wrekin attach wrekin-01CN"},
		Ready:          &Ready{Confirmed: true, Detail: "prompt visible"},
		CreatedAt:      time.Date(2026, 11, 5, 22, 10, 3, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 11, 5, 22, 10, 41, 0, time.UTC),
	}
	got, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var gotAny, wantAny any
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(example), &wantAny); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		t.Errorf("job JSON differs from the protocol example:\n got %s\nwant %s", got, example)
	}

	var back Job
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, job) {
		t.Errorf("round trip differs:\n got %+v\nwant %+v", back, job)
	}
}

func TestJobAbsentSubObjectsAreNull(t *testing.T) {
	job := Job{ID: "job_1", Harness: "claude-code", CloudSessionID: "s", State: StateAccepted}
	got, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"repository", "workdir", "session", "attention", "ready", "error"} {
		v, present := m[key]
		if !present {
			t.Errorf("key %q missing; it must be present and null", key)
		}
		if v != nil {
			t.Errorf("key %q = %v, want null", key, v)
		}
	}
	var back Job
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if back.Workdir != "" || back.Repository != nil {
		t.Errorf("nulls must decode to zero values: %+v", back)
	}
}

func TestResultShapes(t *testing.T) {
	ok, _ := json.Marshal(NewResult("cmd_1", map[string]string{"at": "now"}))
	var m map[string]any
	_ = json.Unmarshal(ok, &m)
	if m["v"] != float64(1) || m["type"] != "result" || m["id"] != "cmd_1" || m["ok"] != true {
		t.Errorf("ok result header wrong: %s", ok)
	}
	if _, has := m["error"]; has {
		t.Errorf("ok result must not carry an error key: %s", ok)
	}
	if m["data"].(map[string]any)["at"] != "now" {
		t.Errorf("ok result data wrong: %s", ok)
	}

	bad, _ := json.Marshal(NewErrorResult("cmd_2", CodeUnknownJob, "no job of that id"))
	m = nil
	_ = json.Unmarshal(bad, &m)
	if m["ok"] != false {
		t.Errorf("error result ok flag wrong: %s", bad)
	}
	if _, has := m["data"]; has {
		t.Errorf("error result must not carry a data key: %s", bad)
	}
	e := m["error"].(map[string]any)
	if e["code"] != "unknown_job" || e["message"] != "no job of that id" {
		t.Errorf("error body wrong: %s", bad)
	}
}

func TestErrorCodesAreTheProtocolStrings(t *testing.T) {
	want := map[string]string{
		CodeNodeOffline: "node_offline", CodeNodeTimeout: "node_timeout", CodeUnknownNode: "unknown_node",
		CodeInvalidParams: "invalid_params", CodeUnknownJob: "unknown_job", CodeRepositoryRequired: "repository_required",
		CodeDirectoryNotAllowed: "directory_not_allowed", CodePathEscapesRoot: "path_escapes_root",
		CodeHarnessUnavailable: "harness_unavailable", CodeTerminalUnavailable: "terminal_unavailable",
		CodeConcurrencyLimit: "concurrency_limit", CodeAttentionNotPending: "attention_not_pending",
		CodeNotApproved: "not_approved", CodeCloneFailed: "clone_failed", CodeGitPromptBlocked: "git_prompt_blocked",
		CodeWorktreeFailed: "worktree_failed", CodeHarnessExited: "harness_exited", CodeSessionGone: "session_gone",
		CodeInternal: "internal",
	}
	if len(want) != 19 {
		t.Fatalf("the protocol table has 19 codes, test lists %d", len(want))
	}
	for got, exp := range want {
		if got != exp {
			t.Errorf("code %q != %q", got, exp)
		}
	}
}

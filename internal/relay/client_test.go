package relay

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/protocol"
)

type recordingHandler struct {
	calls atomic.Int32
	seen  chan protocol.Command
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{seen: make(chan protocol.Command, 16)}
}

func (h *recordingHandler) Handle(ctx context.Context, cmd protocol.Command) protocol.Result {
	h.calls.Add(1)
	h.seen <- cmd
	return protocol.NewResult(cmd.ID, protocol.PingResult{At: "now"})
}

type testClient struct {
	relay   *fakeRelay
	client  *Client
	handler *recordingHandler
	id      *identity.Identity
	done    chan error
}

func start(t *testing.T, tweak func(*Options)) *testClient {
	t.Helper()
	return startWith(t, nil, tweak)
}

// startWith is start with a hook on the fake relay before the client dials.
func startWith(t *testing.T, tweakRelay func(*fakeRelay), tweak func(*Options)) *testClient {
	t.Helper()
	id, err := identity.Generate("box")
	if err != nil {
		t.Fatal(err)
	}
	id.NodeID = "node_test"
	relay := newFakeRelay(t, id)
	if tweakRelay != nil {
		tweakRelay(relay)
	}
	handler := newRecordingHandler()
	opts := Options{
		URL:            "ws://relay.test/connect",
		Identity:       id,
		Name:           "box",
		AgentVersion:   "0.0.1-test",
		Harnesses:      []protocol.HarnessInfo{{ID: "claude-code", Available: true, Version: "9.9.9"}},
		Handler:        handler,
		Dial:           relay.dialer(),
		BackoffInitial: 10 * time.Millisecond,
		BackoffCap:     time.Second,
		PongTimeout:    200 * time.Millisecond,
	}
	if tweak != nil {
		tweak(&opts)
	}
	client := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	return &testClient{relay: relay, client: client, handler: handler, id: id, done: done}
}

func waitStatus(t *testing.T, c *Client, want Status) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := c.State(); st.Status == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("client did not reach %s; state %+v", want, c.State())
	return State{}
}

func TestHandshakeSignsChallenge(t *testing.T) {
	tc := start(t, nil)
	tc.relay.waitConnected(5 * time.Second)
	st := waitStatus(t, tc.client, Connected)
	if st.AccountID != "usr_1" || st.ConnectedSince.IsZero() || st.LastError != "" {
		t.Errorf("state = %+v", st)
	}
	hello := tc.relay.firstHello()
	if hello.V != 1 || hello.NodeID != "node_test" || hello.PublicKey != tc.id.PublicKeyBase64() || hello.Name != "box" || hello.AgentVersion != "0.0.1-test" {
		t.Errorf("hello = %+v", hello)
	}
	if len(hello.Harnesses) != 1 || hello.Harnesses[0].ID != "claude-code" || !hello.Harnesses[0].Available {
		t.Errorf("hello harnesses = %+v", hello.Harnesses)
	}
	if hello.Platform.OS == "" || hello.Platform.Arch == "" || hello.Platform.Hostname == "" {
		t.Errorf("hello platform = %+v", hello.Platform)
	}
}

func TestCommandIsHandledAndAnswered(t *testing.T) {
	tc := start(t, nil)
	rc := tc.relay.waitConnected(5 * time.Second)
	tc.relay.send(rc, "cmd_1", "ping", map[string]any{})
	cmd := <-tc.handler.seen
	if cmd.ID != "cmd_1" || cmd.Name != "ping" {
		t.Errorf("handler got %+v", cmd)
	}
	res := tc.relay.waitResult(5 * time.Second)
	if res.V != 1 || res.Type != "result" || res.ID != "cmd_1" || !res.OK {
		t.Errorf("result = %+v", res)
	}
	data, _ := json.Marshal(res.Data)
	if string(data) != `{"at":"now"}` {
		t.Errorf("result data = %s", data)
	}
}

func TestDuplicateCommandIdAnsweredOnce(t *testing.T) {
	tc := start(t, nil)
	rc := tc.relay.waitConnected(5 * time.Second)
	tc.relay.send(rc, "cmd_dup", "ping", map[string]any{})
	tc.relay.send(rc, "cmd_dup", "ping", map[string]any{})
	first := tc.relay.waitResult(5 * time.Second)
	second := tc.relay.waitResult(5 * time.Second)
	if first.ID != "cmd_dup" || second.ID != "cmd_dup" || !first.OK || !second.OK {
		t.Errorf("results = %+v %+v", first, second)
	}
	time.Sleep(50 * time.Millisecond)
	if n := tc.handler.calls.Load(); n != 1 {
		t.Errorf("handler called %d times, want 1", n)
	}
	// A duplicate arriving on a later connection is answered from the cache too.
	tc.relay.closeWith(rc, 1000)
	rc2 := tc.relay.waitConnected(5 * time.Second)
	tc.relay.send(rc2, "cmd_dup", "ping", map[string]any{})
	third := tc.relay.waitResult(5 * time.Second)
	if third.ID != "cmd_dup" || tc.handler.calls.Load() != 1 {
		t.Errorf("cached answer after reconnect: %+v calls=%d", third, tc.handler.calls.Load())
	}
}

func TestReconnectsAfterServerClose(t *testing.T) {
	tc := start(t, nil)
	rc := tc.relay.waitConnected(5 * time.Second)
	waitStatus(t, tc.client, Connected)
	tc.relay.closeWith(rc, 1000)
	rc2 := tc.relay.waitConnected(5 * time.Second)
	if rc2 == rc {
		t.Fatal("expected a new connection")
	}
	st := waitStatus(t, tc.client, Connected)
	if tc.relay.helloCount() != 2 {
		t.Errorf("hello count %d, want 2", tc.relay.helloCount())
	}
	if st.Attempts != 0 {
		t.Errorf("attempts must reset after welcome, got %d", st.Attempts)
	}
}

func TestAuthFailureWaitsCap(t *testing.T) {
	var tc *testClient
	tc = start(t, func(o *Options) { o.BackoffCap = time.Second })
	tc.relay.setAuthFail(true)
	// The first attempt is refused with 4401 ...
	deadline := time.Now().Add(5 * time.Second)
	for tc.relay.helloCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if tc.relay.helloCount() != 1 {
		t.Fatalf("hello count %d", tc.relay.helloCount())
	}
	// ... and the node waits the whole cap before trying again.
	time.Sleep(200 * time.Millisecond)
	if tc.relay.helloCount() != 1 {
		t.Fatalf("the node retried within 200 ms after 4401 (hellos: %d)", tc.relay.helloCount())
	}
	st := tc.client.State()
	if st.Status == Connected || st.LastError == "" {
		t.Errorf("state after 4401 = %+v", st)
	}
	tc.relay.setAuthFail(false)
	tc.relay.waitConnected(5 * time.Second)
	waitStatus(t, tc.client, Connected)
}

func TestPongTimeoutReconnects(t *testing.T) {
	tc := start(t, func(o *Options) {
		o.PingInterval = 50 * time.Millisecond
		o.PongTimeout = 100 * time.Millisecond
	})
	tc.relay.setAutoPong(false)
	rc := tc.relay.waitConnected(5 * time.Second)
	waitStatus(t, tc.client, Connected)
	// Without pongs the client must drop the socket and come back.
	select {
	case <-rc.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the client kept a connection without pongs")
	}
	tc.relay.setAutoPong(true)
	tc.relay.waitConnected(5 * time.Second)
	waitStatus(t, tc.client, Connected)
	if tc.relay.helloCount() < 2 {
		t.Errorf("hello count %d", tc.relay.helloCount())
	}
}

func TestUnknownCommandTypesAreIgnored(t *testing.T) {
	tc := start(t, nil)
	rc := tc.relay.waitConnected(5 * time.Second)
	if err := writeJSON(context.Background(), rc.ws, map[string]any{"v": 1, "type": "future-thing", "x": 1}); err != nil {
		t.Fatal(err)
	}
	tc.relay.send(rc, "cmd_after", "ping", map[string]any{})
	res := tc.relay.waitResult(5 * time.Second)
	if res.ID != "cmd_after" {
		t.Errorf("result = %+v", res)
	}
	tc.relay.noResultWithin(50 * time.Millisecond)
	_ = errNotUsed
}

// The welcome must name this node and, once the identity knows its account,
// this account: a relay that welcomes another identity is a protocol error
// and the client never reports Connected.
func TestWelcomeForAnotherIdentityDisconnects(t *testing.T) {
	cases := []struct {
		name, welcomeNode, welcomeAccount, identityAccount string
		ok                                                 bool
	}{
		{"other node", "node_other", "usr_1", "", false},
		{"other account", "node_test", "usr_other", "usr_1", false},
		{"same identity", "node_test", "usr_1", "usr_1", true},
		{"account not yet known", "node_test", "usr_anything", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tc := startWith(t,
				func(f *fakeRelay) { f.setWelcome(c.welcomeNode, c.welcomeAccount) },
				func(o *Options) { o.Identity.AccountID = c.identityAccount })
			rc := tc.relay.waitConnected(5 * time.Second) // the relay has sent its welcome
			if c.ok {
				st := waitStatus(t, tc.client, Connected)
				if st.AccountID != c.welcomeAccount {
					t.Errorf("state = %+v", st)
				}
				return
			}
			select {
			case <-rc.closed:
			case <-time.After(5 * time.Second):
				t.Fatal("the client kept a connection welcomed for another identity")
			}
			deadline := time.Now().Add(5 * time.Second)
			for tc.client.State().LastError == "" && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			st := tc.client.State()
			if st.Status == Connected || !st.ConnectedSince.IsZero() || !strings.Contains(st.LastError, "identity") {
				t.Errorf("state after a foreign welcome = %+v", st)
			}
			// It never flips to Connected afterwards either.
			until := time.Now().Add(200 * time.Millisecond)
			for time.Now().Before(until) {
				if st := tc.client.State(); st.Status == Connected {
					t.Fatalf("client reported Connected: %+v", st)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/protocol"
)

// memListener hands out in-memory connections: no port is ever bound, so the
// tests run in sandboxes that forbid listening sockets.
type memListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr{} }

// dial creates a pipe and hands the server end to Accept.
func (l *memListener) dial(ctx context.Context) (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "mem" }

// fakeRelay is an in-process relay implementing the PROTOCOL.md handshake.
type fakeRelay struct {
	t      *testing.T
	pubB64 string
	nodeID string

	ln     *memListener
	server *http.Server

	mu          sync.Mutex
	authFail    bool
	autoPong    bool
	pingSeconds int
	hellos      []protocol.Hello
	conns       []*relayConn

	connected chan *relayConn
	results   chan protocol.Result
}

// relayConn is one accepted node socket.
type relayConn struct {
	ws     *websocket.Conn
	closed chan struct{}
}

func newFakeRelay(t *testing.T, id *identity.Identity) *fakeRelay {
	t.Helper()
	f := &fakeRelay{
		t:           t,
		pubB64:      id.PublicKeyBase64(),
		nodeID:      id.NodeID,
		ln:          newMemListener(),
		autoPong:    true,
		pingSeconds: 30,
		connected:   make(chan *relayConn, 16),
		results:     make(chan protocol.Result, 16),
	}
	f.server = &http.Server{Handler: http.HandlerFunc(f.handle)}
	go func() { _ = f.server.Serve(f.ln) }()
	t.Cleanup(func() {
		_ = f.ln.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = f.server.Shutdown(ctx)
	})
	return f
}

// dialer returns the Options.Dial hook that connects through the pipe.
func (f *fakeRelay) dialer() func(ctx context.Context, url string) (*websocket.Conn, error) {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return f.ln.dial(ctx) },
	}}
	return func(ctx context.Context, url string) (*websocket.Conn, error) {
		c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
		return c, err
	}
}

func (f *fakeRelay) handle(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	rc := &relayConn{ws: ws, closed: make(chan struct{})}
	defer close(rc.closed)
	ctx := r.Context()

	if r.URL.Query().Get("node") != f.nodeID {
		_ = ws.Close(websocket.StatusCode(protocol.CloseUnknownNode), "unknown node")
		return
	}
	var hello protocol.Hello
	if err := readAs(ctx, ws, &hello); err != nil || hello.Type != protocol.TypeHello {
		_ = ws.Close(websocket.StatusCode(protocol.CloseProtocolError), "expected hello")
		return
	}
	f.mu.Lock()
	f.hellos = append(f.hellos, hello)
	authFail := f.authFail
	pingSeconds := f.pingSeconds
	f.mu.Unlock()

	nonceBytes := make([]byte, 32)
	_, _ = rand.Read(nonceBytes)
	nonce := base64.StdEncoding.EncodeToString(nonceBytes)
	if err := writeJSON(ctx, ws, protocol.Challenge{Envelope: protocol.Envelope{V: 1, Type: protocol.TypeChallenge}, Nonce: nonce}); err != nil {
		return
	}
	var auth protocol.Auth
	if err := readAs(ctx, ws, &auth); err != nil || auth.Type != protocol.TypeAuth {
		_ = ws.Close(websocket.StatusCode(protocol.CloseProtocolError), "expected auth")
		return
	}
	if authFail || !identity.Verify(f.pubB64, f.nodeID, nonce, auth.Signature) {
		_ = writeJSON(ctx, ws, protocol.ErrorMessage{Envelope: protocol.Envelope{V: 1, Type: protocol.TypeError}, Code: "auth_failed", Message: "bad signature"})
		_ = ws.Close(websocket.StatusCode(protocol.CloseAuthFailed), "authentication failed")
		return
	}
	if err := writeJSON(ctx, ws, protocol.Welcome{Envelope: protocol.Envelope{V: 1, Type: protocol.TypeWelcome}, AccountID: "usr_1", NodeID: f.nodeID, PingSeconds: pingSeconds}); err != nil {
		return
	}
	f.mu.Lock()
	f.conns = append(f.conns, rc)
	f.mu.Unlock()
	f.connected <- rc

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		msg, err := protocol.Decode(data)
		if err != nil {
			continue
		}
		switch m := msg.(type) {
		case *protocol.Ping:
			f.mu.Lock()
			pong := f.autoPong
			f.mu.Unlock()
			if pong {
				_ = writeJSON(ctx, ws, protocol.NewPong())
			}
		case *protocol.Result:
			f.results <- *m
		}
	}
}

func readAs(ctx context.Context, ws *websocket.Conn, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, data, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, data)
}

// waitConnected returns the next authenticated connection.
func (f *fakeRelay) waitConnected(timeout time.Duration) *relayConn {
	f.t.Helper()
	select {
	case rc := <-f.connected:
		return rc
	case <-time.After(timeout):
		f.t.Fatal("the node did not connect in time")
		return nil
	}
}

func (f *fakeRelay) noConnectionWithin(d time.Duration) {
	f.t.Helper()
	select {
	case <-f.connected:
		f.t.Fatal("the node reconnected although it should have waited")
	case <-time.After(d):
	}
}

// send delivers a command to the node.
func (f *fakeRelay) send(rc *relayConn, id, name string, params any) {
	f.t.Helper()
	raw, _ := json.Marshal(params)
	cmd := protocol.Command{Envelope: protocol.Envelope{V: 1, Type: protocol.TypeCommand}, ID: id, Name: name, Params: raw}
	if err := writeJSON(context.Background(), rc.ws, cmd); err != nil {
		f.t.Fatalf("send command: %v", err)
	}
}

func (f *fakeRelay) waitResult(timeout time.Duration) protocol.Result {
	f.t.Helper()
	select {
	case r := <-f.results:
		return r
	case <-time.After(timeout):
		f.t.Fatal("no result in time")
		return protocol.Result{}
	}
}

func (f *fakeRelay) noResultWithin(d time.Duration) {
	f.t.Helper()
	select {
	case r := <-f.results:
		f.t.Fatalf("unexpected result %+v", r)
	case <-time.After(d):
	}
}

func (f *fakeRelay) setAuthFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authFail = v
}

func (f *fakeRelay) setAutoPong(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoPong = v
}

func (f *fakeRelay) helloCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hellos)
}

func (f *fakeRelay) firstHello() protocol.Hello {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hellos[0]
}

// closeWith closes a node socket with the given status code.
func (f *fakeRelay) closeWith(rc *relayConn, code int) {
	_ = rc.ws.Close(websocket.StatusCode(code), "test close")
}

var errNotUsed = errors.New("not used")

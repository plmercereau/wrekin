package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// memListener is an in-memory net.Listener so the protocol can be tested in
// sandboxes that forbid unix sockets.
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

func (l *memListener) Addr() net.Addr { return &net.UnixAddr{Name: "mem", Net: "unix"} }

func (l *memListener) dial() (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, ErrNotRunning
	case <-time.After(time.Second):
		return nil, errors.New("accept did not happen")
	}
}

type statusPayload struct {
	Version string `json:"version"`
	Jobs    int    `json:"jobs"`
}

func TestControlStatusRoundTrip(t *testing.T) {
	ln := newMemListener()
	srv := NewServer(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "status":
			return statusPayload{Version: "1.2.3", Jobs: 2}, nil
		case "echo":
			var p map[string]any
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			return p, nil
		default:
			return nil, errors.New("unknown method " + method)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	client := NewClientWithDialer(ln.dial)
	var st statusPayload
	if err := client.Call(ctx, "status", nil, &st); err != nil {
		t.Fatalf("Call(status): %v", err)
	}
	if st.Version != "1.2.3" || st.Jobs != 2 {
		t.Errorf("status = %+v", st)
	}
	var echoed map[string]any
	if err := client.Call(ctx, "echo", map[string]any{"dryRun": true}, &echoed); err != nil {
		t.Fatalf("Call(echo): %v", err)
	}
	if echoed["dryRun"] != true {
		t.Errorf("echo = %v", echoed)
	}
	err := client.Call(ctx, "nope", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown method nope") {
		t.Errorf("error not propagated: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestClientReportsNotRunning(t *testing.T) {
	client := NewClient(filepath.Join(t.TempDir(), "absent.sock"))
	err := client.Call(context.Background(), "status", nil, nil)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestListenUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp(os.TempDir(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "c.sock")
	// A stale socket file is replaced.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(path)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("unix sockets are not permitted here (sandboxed?): %v", err)
		}
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %o, want 0600", info.Mode().Perm())
	}
	srv := NewServer(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		return map[string]string{"pong": method}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx, ln) }()
	var out map[string]string
	if err := NewClient(path).Call(ctx, "ping", nil, &out); err != nil {
		t.Fatalf("Call over unix socket: %v", err)
	}
	if out["pong"] != "ping" {
		t.Errorf("out = %v", out)
	}
}

// Package control is the local channel between the CLI and the daemon: one
// JSON request and one JSON response per connection over a unix socket that
// only the user can open.
package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/plmercereau/wrekin/internal/protocol"
	"github.com/plmercereau/wrekin/internal/relay"
)

// ErrNotRunning means no daemon answers on the socket.
var ErrNotRunning = errors.New("the wrekin daemon is not running")

// Method names.
const (
	MethodStatus   = "status"
	MethodSessions = "sessions"
	MethodCleanup  = "cleanup"
	MethodShutdown = "shutdown"
)

// Request is one call.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response answers a Request.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// StatusInfo is the result of "status".
type StatusInfo struct {
	Version   string         `json:"version"`
	PID       int            `json:"pid"`
	StartedAt time.Time      `json:"startedAt"`
	NodeID    string         `json:"nodeId"`
	Name      string         `json:"name"`
	AccountID string         `json:"accountId"`
	Relay     relay.State    `json:"relay"`
	Jobs      map[string]int `json:"jobs"`
}

// SessionsResult is the result of "sessions".
type SessionsResult struct {
	Jobs []protocol.Job `json:"jobs"`
}

// CleanupParams are the parameters of "cleanup".
type CleanupParams struct {
	DryRun bool `json:"dryRun"`
}

// CleanupReport lists what was (or would be) removed.
type CleanupReport struct {
	DryRun    bool     `json:"dryRun"`
	Sessions  []string `json:"sessions"`
	Worktrees []string `json:"worktrees"`
	Clones    []string `json:"clones"`
	Jobs      []string `json:"jobs"`
	Skipped   []string `json:"skipped"`
	Errors    []string `json:"errors"`
}

// Handler serves one method.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// Server accepts connections and answers requests.
type Server struct {
	handler Handler
}

// NewServer builds a server.
func NewServer(h Handler) *Server { return &Server{handler: h} }

// Listen creates the unix socket at path (mode 0600), replacing a stale file.
func Listen(path string) (net.Listener, error) {
	if len(path) >= 100 {
		return nil, fmt.Errorf("socket path %q is too long for a unix socket", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// Serve answers requests until ctx is done or the listener fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		writeResponse(conn, Response{Error: "malformed request: " + err.Error()})
		return
	}
	result, err := s.handler(ctx, req.Method, req.Params)
	if err != nil {
		writeResponse(conn, Response{Error: err.Error()})
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		writeResponse(conn, Response{Error: "encode result: " + err.Error()})
		return
	}
	writeResponse(conn, Response{OK: true, Result: raw})
}

func writeResponse(conn net.Conn, res Response) {
	data, _ := json.Marshal(res)
	_, _ = conn.Write(append(data, '\n'))
}

// Client calls the daemon.
type Client struct {
	dial func() (net.Conn, error)
}

// NewClient talks to the unix socket at path.
func NewClient(path string) *Client {
	return &Client{dial: func() (net.Conn, error) {
		// No socket file, no daemon: said before dialing, because a path too
		// long for a unix socket fails with EINVAL rather than ENOENT.
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w (%s)", ErrNotRunning, path)
		}
		conn, err := net.DialTimeout("unix", path, 2*time.Second)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
				return nil, fmt.Errorf("%w (%s)", ErrNotRunning, path)
			}
			return nil, err
		}
		return conn, nil
	}}
}

// NewClientWithDialer uses a custom connection factory (tests).
func NewClientWithDialer(dial func() (net.Conn, error)) *Client { return &Client{dial: dial} }

// Call sends one request and decodes the result into out (which may be nil).
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	req := Request{Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = raw
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	var res Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	if out != nil && len(res.Result) > 0 {
		return json.Unmarshal(res.Result, out)
	}
	return nil
}

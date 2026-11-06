// Package relay keeps the node's single outbound WebSocket to the relay:
// signed handshake, liveness pings, command dispatch with de-duplication and
// reconnection with exponential backoff and full jitter.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/protocol"
)

// Handler answers commands. It is never called twice for one command id.
type Handler interface {
	Handle(ctx context.Context, cmd protocol.Command) protocol.Result
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, cmd protocol.Command) protocol.Result

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, cmd protocol.Command) protocol.Result {
	return f(ctx, cmd)
}

// Options configures a Client.
type Options struct {
	URL          string
	Identity     *identity.Identity
	Name         string
	AgentVersion string
	Harnesses    []protocol.HarnessInfo
	Platform     protocol.Platform
	Handler      Handler
	Logger       *slog.Logger

	// Dial replaces the WebSocket dialer (tests).
	Dial func(ctx context.Context, url string) (*websocket.Conn, error)

	// Backoff tuning; zero means the protocol defaults (1 s, 60 s).
	BackoffInitial time.Duration
	BackoffCap     time.Duration
	// HandshakeTimeout bounds hello→welcome (default 10 s).
	HandshakeTimeout time.Duration
	// PingInterval overrides the relay's pingSeconds (tests).
	PingInterval time.Duration
	// PongTimeout is how long to wait for a pong (default 10 s).
	PongTimeout time.Duration
	// DedupTTL is how long command ids are remembered (default 10 min).
	DedupTTL time.Duration
}

// Status is the connection status.
type Status string

// Connection statuses.
const (
	Disconnected Status = "disconnected"
	Connecting   Status = "connecting"
	Connected    Status = "connected"
)

// State is a snapshot of the connection.
type State struct {
	Status         Status    `json:"status"`
	LastError      string    `json:"lastError,omitempty"`
	ConnectedSince time.Time `json:"connectedSince,omitempty"`
	AccountID      string    `json:"accountId,omitempty"`
	Attempts       int       `json:"attempts"`
}

// Client is the relay connection.
type Client struct {
	o Options

	mu    sync.Mutex
	state State

	dedup *dedupCache
}

// New builds a client; Run starts it.
func New(o Options) *Client {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.BackoffInitial <= 0 {
		o.BackoffInitial = time.Second
	}
	if o.BackoffCap <= 0 {
		o.BackoffCap = 60 * time.Second
	}
	if o.HandshakeTimeout <= 0 {
		o.HandshakeTimeout = 10 * time.Second
	}
	if o.PongTimeout <= 0 {
		o.PongTimeout = 10 * time.Second
	}
	if o.DedupTTL <= 0 {
		o.DedupTTL = 10 * time.Minute
	}
	if o.Platform == (protocol.Platform{}) {
		host, _ := os.Hostname()
		o.Platform = protocol.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host}
	}
	if o.Dial == nil {
		o.Dial = func(ctx context.Context, u string) (*websocket.Conn, error) {
			c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{
				HTTPHeader: map[string][]string{"User-Agent": {"wrekin/" + o.AgentVersion}},
			})
			return c, err
		}
	}
	return &Client{o: o, state: State{Status: Disconnected}, dedup: newDedupCache(o.DedupTTL)}
}

// State returns a snapshot.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *Client) setState(fn func(*State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.state)
}

// Run connects, serves and reconnects until ctx is done.
func (c *Client) Run(ctx context.Context) error {
	attempt := 0 // consecutive attempts without a welcome
	for {
		c.setState(func(s *State) { s.Status = Connecting; s.Attempts = attempt })
		welcomed, code, err := c.session(ctx)
		if ctx.Err() != nil {
			c.setState(func(s *State) { s.Status = Disconnected; s.ConnectedSince = time.Time{} })
			return ctx.Err()
		}
		if welcomed {
			attempt = 0
		}
		msg := "connection closed"
		if err != nil {
			msg = err.Error()
		}
		c.setState(func(s *State) {
			s.Status = Disconnected
			s.ConnectedSince = time.Time{}
			s.LastError = msg
		})
		c.o.Logger.Warn("relay disconnected", "err", msg, "closeCode", code)

		var wait time.Duration
		switch code {
		case protocol.CloseAuthFailed, protocol.CloseUnknownNode:
			wait = c.o.BackoffCap
			c.o.Logger.Error("the relay rejected this node; an operator must fix the pairing", "closeCode", code)
		default:
			wait = backoff(c.o.BackoffInitial, c.o.BackoffCap, attempt)
		}
		attempt++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// backoff is exponential with full jitter.
func backoff(initial, cap time.Duration, attempt int) time.Duration {
	d := initial
	for i := 0; i < attempt && d < cap; i++ {
		d *= 2
	}
	if d > cap {
		d = cap
	}
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d)))
}

// session runs one connection: handshake then serve. It reports whether the
// handshake completed, the close code received from the relay (-1 when the
// connection ended without one) and the reason it ended.
func (c *Client) session(ctx context.Context) (welcomed bool, code int, err error) {
	u, err := url.Parse(c.o.URL)
	if err != nil {
		return false, -1, fmt.Errorf("relay url: %w", err)
	}
	q := u.Query()
	q.Set("node", c.o.Identity.NodeID)
	u.RawQuery = q.Encode()

	dialCtx, cancel := context.WithTimeout(ctx, c.o.HandshakeTimeout)
	ws, err := c.o.Dial(dialCtx, u.String())
	cancel()
	if err != nil {
		return false, -1, fmt.Errorf("dial: %w", err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(4 << 20)

	pingSeconds, err := c.handshake(ctx, ws)
	if err != nil {
		return false, int(websocket.CloseStatus(err)), err
	}
	c.setState(func(s *State) {
		s.Status = Connected
		s.ConnectedSince = time.Now()
		s.LastError = ""
		s.Attempts = 0
	})
	c.o.Logger.Info("relay connected", "url", c.o.URL)
	err = c.serve(ctx, ws, pingSeconds)
	return true, int(websocket.CloseStatus(err)), err
}

// handshake performs hello → challenge → auth → welcome.
func (c *Client) handshake(ctx context.Context, ws *websocket.Conn) (pingSeconds int, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.o.HandshakeTimeout)
	defer cancel()

	hello := protocol.Hello{
		Envelope:     protocol.Envelope{V: protocol.Version, Type: protocol.TypeHello},
		NodeID:       c.o.Identity.NodeID,
		PublicKey:    c.o.Identity.PublicKeyBase64(),
		Name:         c.o.Name,
		AgentVersion: c.o.AgentVersion,
		Platform:     c.o.Platform,
		Harnesses:    c.o.Harnesses,
	}
	if hello.Harnesses == nil {
		hello.Harnesses = []protocol.HarnessInfo{}
	}
	if err := writeMessage(ctx, ws, hello); err != nil {
		return 0, fmt.Errorf("send hello: %w", err)
	}
	msg, err := readMessage(ctx, ws)
	if err != nil {
		return 0, fmt.Errorf("await challenge: %w", err)
	}
	challenge, ok := msg.(*protocol.Challenge)
	if !ok || challenge.Nonce == "" {
		if em, isErr := msg.(*protocol.ErrorMessage); isErr {
			return 0, c.afterError(ctx, ws, em)
		}
		_ = ws.Close(websocket.StatusProtocolError, "expected challenge")
		return 0, errors.New("protocol error: expected a challenge")
	}
	auth := protocol.NewAuth(c.o.Identity.Sign(c.o.Identity.NodeID, challenge.Nonce))
	if err := writeMessage(ctx, ws, auth); err != nil {
		return 0, fmt.Errorf("send auth: %w", err)
	}
	msg, err = readMessage(ctx, ws)
	if err != nil {
		return 0, fmt.Errorf("await welcome: %w", err)
	}
	switch m := msg.(type) {
	case *protocol.Welcome:
		// The welcome must name this node and, once pairing recorded it, this
		// account; anything else is not a connection for this identity.
		if m.NodeID != c.o.Identity.NodeID || (c.o.Identity.AccountID != "" && m.AccountID != c.o.Identity.AccountID) {
			c.o.Logger.Error("relay welcomed a different identity", "nodeId", m.NodeID, "accountId", m.AccountID, "wantNodeId", c.o.Identity.NodeID, "wantAccountId", c.o.Identity.AccountID)
			_ = ws.Close(websocket.StatusProtocolError, "welcome names another identity")
			return 0, errors.New("protocol error: the relay welcomed a different identity")
		}
		if m.PingSeconds <= 0 {
			m.PingSeconds = 30
		}
		c.setState(func(s *State) { s.AccountID = m.AccountID })
		return m.PingSeconds, nil
	case *protocol.ErrorMessage:
		return 0, c.afterError(ctx, ws, m)
	default:
		_ = ws.Close(websocket.StatusProtocolError, "expected welcome")
		return 0, errors.New("protocol error: expected welcome")
	}
}

// afterError waits briefly for the close frame that follows an error message
// so that the close code drives the backoff.
func (c *Client) afterError(ctx context.Context, ws *websocket.Conn, em *protocol.ErrorMessage) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, _, err := ws.Read(ctx)
	if code := websocket.CloseStatus(err); code != -1 {
		return fmt.Errorf("relay refused the node (%s: %s): %w", em.Code, em.Message, err)
	}
	return fmt.Errorf("relay refused the node (%s: %s)", em.Code, em.Message)
}

// serve handles commands and liveness until the connection ends.
func (c *Client) serve(ctx context.Context, ws *websocket.Conn, pingSeconds int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	interval := time.Duration(pingSeconds) * time.Second
	if c.o.PingInterval > 0 {
		interval = c.o.PingInterval
	}
	pongs := make(chan struct{}, 1)
	readErr := make(chan error, 1)

	go func() {
		for {
			msg, err := readMessage(ctx, ws)
			if err != nil {
				readErr <- err
				return
			}
			switch m := msg.(type) {
			case *protocol.Pong:
				select {
				case pongs <- struct{}{}:
				default:
				}
			case *protocol.Command:
				c.dispatch(ctx, ws, *m)
			case *protocol.Ping:
				_ = writeMessage(ctx, ws, protocol.NewPong())
			case *protocol.ErrorMessage:
				c.o.Logger.Warn("relay error", "code", m.Code, "message", m.Message)
			case *protocol.UnknownMessage:
				c.o.Logger.Debug("ignoring unknown frame", "type", m.Type)
			}
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var pongDeadline <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			_ = ws.Close(websocket.StatusNormalClosure, "shutting down")
			return ctx.Err()
		case err := <-readErr:
			return err
		case <-ticker.C:
			if err := writeMessage(ctx, ws, protocol.NewPing()); err != nil {
				return fmt.Errorf("send ping: %w", err)
			}
			if pongDeadline == nil {
				pongDeadline = time.After(c.o.PongTimeout)
			}
		case <-pongs:
			pongDeadline = nil
		case <-pongDeadline:
			_ = ws.Close(websocket.StatusGoingAway, "no pong")
			return errors.New("no pong from the relay within " + c.o.PongTimeout.String())
		}
	}
}

// dispatch answers a command exactly once, from the cache when its id was
// already seen.
func (c *Client) dispatch(ctx context.Context, ws *websocket.Conn, cmd protocol.Command) {
	entry, fresh := c.dedup.claim(cmd.ID)
	if !fresh {
		c.o.Logger.Info("duplicate command answered from cache", "id", cmd.ID)
		go func() {
			select {
			case <-entry.done:
				_ = writeMessage(ctx, ws, entry.result)
			case <-ctx.Done():
			}
		}()
		return
	}
	go func() {
		res := c.o.Handler.Handle(ctx, cmd)
		res.V = protocol.Version
		res.Type = protocol.TypeResult
		res.ID = cmd.ID
		c.dedup.complete(entry, res)
		if err := writeMessage(ctx, ws, res); err != nil {
			c.o.Logger.Warn("result not delivered", "id", cmd.ID, "err", err)
		}
	}()
}

func writeMessage(ctx context.Context, ws *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, data)
}

func readMessage(ctx context.Context, ws *websocket.Conn) (any, error) {
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ != websocket.MessageText {
			continue
		}
		msg, err := protocol.Decode(data)
		if err != nil {
			// Malformed frames are logged by the caller's logger level; skip.
			continue
		}
		return msg, nil
	}
}

// dedupCache remembers command ids for a while.
type dedupCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]*dedupEntry
}

type dedupEntry struct {
	seen   time.Time
	done   chan struct{}
	result protocol.Result
}

func newDedupCache(ttl time.Duration) *dedupCache {
	return &dedupCache{ttl: ttl, m: map[string]*dedupEntry{}}
}

// claim registers id; fresh is false when it was already known.
func (d *dedupCache) claim(id string) (*dedupEntry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for k, e := range d.m {
		if now.Sub(e.seen) > d.ttl {
			delete(d.m, k)
		}
	}
	if e, ok := d.m[id]; ok {
		return e, false
	}
	e := &dedupEntry{seen: now, done: make(chan struct{})}
	d.m[id] = e
	return e, true
}

func (d *dedupCache) complete(e *dedupEntry, res protocol.Result) {
	d.mu.Lock()
	e.result = res
	d.mu.Unlock()
	close(e.done)
}

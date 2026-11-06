package pairing

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plmercereau/wrekin/internal/identity"
)

// handlerTransport serves requests in-process, without a listening socket.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// fakeAuthServer imitates the Better Auth device endpoints and the node API.
type fakeAuthServer struct {
	t *testing.T

	mu          sync.Mutex
	tokenPolls  int
	registered  map[string]any
	registerHdr string
	signedOut   string
	unregister  map[string]any
	pubKey      ed25519.PublicKey
	unregOK     bool
}

func (f *fakeAuthServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/api/auth/device/code":
		if r.Method != http.MethodPost || !strings.Contains(string(body), "client_id=wrekin-agent") || !strings.Contains(string(body), "scope=node") {
			http.Error(w, "bad request "+string(body), 400)
			return
		}
		writeJSON(w, 200, map[string]any{
			"device_code": "dev_123", "user_code": "WXYZ-1234",
			"verification_uri": "https://wrekin.test/device", "verification_uri_complete": "https://wrekin.test/device?user_code=WXYZ-1234",
			"expires_in": 600, "interval": 1,
		})
	case "/api/auth/device/token":
		if !strings.Contains(string(body), "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code") || !strings.Contains(string(body), "device_code=dev_123") {
			http.Error(w, "bad token request "+string(body), 400)
			return
		}
		f.tokenPolls++
		switch f.tokenPolls {
		case 1, 2:
			writeJSON(w, 400, map[string]any{"error": "authorization_pending"})
		case 3:
			writeJSON(w, 400, map[string]any{"error": "slow_down"})
		default:
			writeJSON(w, 200, map[string]any{"access_token": "tok_abc", "token_type": "Bearer"})
		}
	case "/api/nodes":
		f.registerHdr = r.Header.Get("Authorization")
		_ = json.Unmarshal(body, &f.registered)
		writeJSON(w, 201, map[string]any{"nodeId": "node_01J", "accountId": "usr_42"})
	case "/api/auth/sign-out":
		f.signedOut = r.Header.Get("Authorization")
		writeJSON(w, 200, map[string]any{"success": true})
	case "/api/nodes/unregister":
		_ = json.Unmarshal(body, &f.unregister)
		sig, _ := base64.StdEncoding.DecodeString(f.unregister["signature"].(string))
		msg := identity.UnregisterMessage(f.unregister["nodeId"].(string), f.unregister["timestamp"].(string))
		f.unregOK = ed25519.Verify(f.pubKey, msg, sig)
		if !f.unregOK {
			http.Error(w, "bad signature", 401)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestPairingFlowAgainstFakeServer(t *testing.T) {
	id, _ := identity.Generate("box")
	srv := &fakeAuthServer{t: t, pubKey: id.PublicKey}
	var sleeps []time.Duration
	var out bytes.Buffer
	p := &Pairer{
		BaseURL:      "https://wrekin.test",
		Client:       &http.Client{Transport: handlerTransport{srv}},
		AgentVersion: "0.1.0",
		Sleep:        func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil },
		Out:          &out,
	}
	nodeID, accountID, err := p.Pair(context.Background(), "box", id)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if nodeID != "node_01J" || accountID != "usr_42" {
		t.Errorf("ids = %q %q", nodeID, accountID)
	}
	if !strings.Contains(out.String(), "WXYZ-1234") || !strings.Contains(out.String(), "https://wrekin.test/device?user_code=WXYZ-1234") {
		t.Errorf("output lacks the code or the URL:\n%s", out.String())
	}
	if srv.tokenPolls != 4 {
		t.Errorf("token polls = %d, want 4", srv.tokenPolls)
	}
	// interval 1 s for the first polls, then 5 s more after slow_down.
	if len(sleeps) != 4 || sleeps[0] != time.Second || sleeps[1] != time.Second || sleeps[2] != time.Second || sleeps[3] != 6*time.Second {
		t.Errorf("sleeps = %v", sleeps)
	}
	if srv.registerHdr != "Bearer tok_abc" {
		t.Errorf("register Authorization = %q", srv.registerHdr)
	}
	if srv.registered["name"] != "box" || srv.registered["publicKey"] != id.PublicKeyBase64() || srv.registered["agentVersion"] != "0.1.0" {
		t.Errorf("register body = %v", srv.registered)
	}
	platform, _ := srv.registered["platform"].(map[string]any)
	if platform["os"] == "" || platform["arch"] == "" || platform["hostname"] == "" {
		t.Errorf("register platform = %v", platform)
	}
	if srv.signedOut != "Bearer tok_abc" {
		t.Errorf("sign-out Authorization = %q", srv.signedOut)
	}
}

func TestPairingDeniedStops(t *testing.T) {
	id, _ := identity.Generate("box")
	denied := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/device/code":
			writeJSON(w, 200, map[string]any{"device_code": "d", "user_code": "U", "verification_uri_complete": "https://x/d", "interval": 1})
		case "/api/auth/device/token":
			writeJSON(w, 400, map[string]any{"error": "access_denied", "error_description": "the user declined"})
		default:
			http.NotFound(w, r)
		}
	})
	p := &Pairer{BaseURL: "https://wrekin.test", Client: &http.Client{Transport: handlerTransport{denied}}, Out: io.Discard,
		Sleep: func(ctx context.Context, d time.Duration) error { return nil }}
	if _, _, err := p.Pair(context.Background(), "box", id); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("err = %v, want access_denied", err)
	}
}

func TestUnregisterSignature(t *testing.T) {
	id, _ := identity.Generate("box")
	id.NodeID = "node_01J"
	srv := &fakeAuthServer{t: t, pubKey: id.PublicKey}
	p := &Pairer{BaseURL: "https://wrekin.test", Client: &http.Client{Transport: handlerTransport{srv}}, Out: io.Discard}
	if err := p.Unregister(context.Background(), id); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if !srv.unregOK || srv.unregister["nodeId"] != "node_01J" {
		t.Errorf("unregister body = %v (signature ok: %v)", srv.unregister, srv.unregOK)
	}
	if _, err := time.Parse(time.RFC3339, srv.unregister["timestamp"].(string)); err != nil {
		t.Errorf("timestamp is not RFC 3339: %v", srv.unregister["timestamp"])
	}
}

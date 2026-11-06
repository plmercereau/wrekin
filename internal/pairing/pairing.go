// Package pairing enrols a node with the cloud through OAuth device
// authorization: the user approves on another device, the node registers its
// public key with the short-lived token, and the token is discarded.
package pairing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/plmercereau/wrekin/internal/identity"
	"github.com/plmercereau/wrekin/internal/protocol"
)

// ClientID is the OAuth client id of the agent.
const ClientID = "wrekin-agent"

const deviceGrant = "urn:ietf:params:oauth:grant-type:device_code"

// Pairer runs the device flow against BaseURL.
type Pairer struct {
	BaseURL      string
	Client       *http.Client
	AgentVersion string
	Platform     protocol.Platform
	// Sleep waits between polls; nil means time.Sleep honouring ctx.
	Sleep func(ctx context.Context, d time.Duration) error
	// Out receives the instructions for the user; nil means os.Stdout.
	Out io.Writer
}

func (p *Pairer) defaults() {
	if p.Client == nil {
		p.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if p.Sleep == nil {
		p.Sleep = func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		}
	}
	if p.Out == nil {
		p.Out = os.Stdout
	}
	if p.Platform == (protocol.Platform{}) {
		host, _ := os.Hostname()
		p.Platform = protocol.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host}
	}
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")
}

// Pair is the package-level convenience with default settings.
func Pair(ctx context.Context, baseURL, name string, id *identity.Identity, out io.Writer) (nodeID, accountID string, err error) {
	p := &Pairer{BaseURL: baseURL, Out: out}
	return p.Pair(ctx, name, id)
}

// Unregister is the package-level convenience with default settings.
func Unregister(ctx context.Context, baseURL string, id *identity.Identity) error {
	p := &Pairer{BaseURL: baseURL}
	return p.Unregister(ctx, id)
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type registerResponse struct {
	NodeID    string `json:"nodeId"`
	AccountID string `json:"accountId"`
}

// Pair runs the whole flow and returns the ids the cloud assigned.
func (p *Pairer) Pair(ctx context.Context, name string, id *identity.Identity) (nodeID, accountID string, err error) {
	p.defaults()

	var code deviceCodeResponse
	form := url.Values{"client_id": {ClientID}, "scope": {"node"}}
	if err := p.postForm(ctx, "/api/auth/device/code", form, &code); err != nil {
		return "", "", fmt.Errorf("start device authorization: %w", err)
	}
	if code.DeviceCode == "" || code.UserCode == "" {
		return "", "", errors.New("the device authorization response is incomplete")
	}
	verification := code.VerificationURIComplete
	if verification == "" {
		verification = code.VerificationURI
	}
	fmt.Fprintf(p.Out, "Open %s on any device and sign in.\n", verification)
	fmt.Fprintf(p.Out, "Pairing code: %s\n", code.UserCode)
	fmt.Fprintf(p.Out, "Node %q, key fingerprint %s. Waiting for approval...\n", name, id.Fingerprint())

	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	if code.ExpiresIn <= 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}
	var token tokenResponse
	for {
		if err := p.Sleep(ctx, interval); err != nil {
			return "", "", err
		}
		if time.Now().After(deadline) {
			return "", "", errors.New("the pairing code expired before it was approved")
		}
		form := url.Values{"grant_type": {deviceGrant}, "device_code": {code.DeviceCode}, "client_id": {ClientID}}
		token = tokenResponse{}
		status, err := p.postFormStatus(ctx, "/api/auth/device/token", form, &token)
		if err != nil {
			return "", "", fmt.Errorf("poll device token: %w", err)
		}
		if status == http.StatusOK && token.AccessToken != "" {
			break
		}
		switch token.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "":
			return "", "", fmt.Errorf("poll device token: unexpected status %d", status)
		default:
			return "", "", fmt.Errorf("pairing refused: %s %s", token.Error, strings.TrimSpace(token.ErrorDescription))
		}
	}

	var reg registerResponse
	// The Worker stores the platform as one string; the hostname stays local.
	body := map[string]any{"name": name, "publicKey": id.PublicKeyBase64(), "agentVersion": p.AgentVersion, "platform": p.Platform.OS + "/" + p.Platform.Arch}
	if err := p.postJSON(ctx, "/api/nodes", token.AccessToken, body, &reg); err != nil {
		return "", "", fmt.Errorf("register node: %w", err)
	}
	if reg.NodeID == "" || reg.AccountID == "" {
		return "", "", errors.New("the node registration response is incomplete")
	}
	// The token is only needed for registration; end its session (best effort).
	_ = p.postJSON(ctx, "/api/auth/sign-out", token.AccessToken, map[string]any{}, nil)
	return reg.NodeID, reg.AccountID, nil
}

// Unregister removes this node from the account, proving possession of the
// private key.
func (p *Pairer) Unregister(ctx context.Context, id *identity.Identity) error {
	p.defaults()
	if !id.Paired() {
		return errors.New("this identity is not paired")
	}
	timestamp := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"nodeId":    id.NodeID,
		"timestamp": timestamp,
		"signature": id.SignMessage(identity.UnregisterMessage(id.NodeID, timestamp)),
	}
	return p.postJSON(ctx, "/api/nodes/unregister", "", body, nil)
}

func (p *Pairer) postForm(ctx context.Context, path string, form url.Values, out any) error {
	status, err := p.postFormStatus(ctx, path, form, out)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("unexpected status %d", status)
	}
	return nil
}

func (p *Pairer) postFormStatus(ctx context.Context, path string, form url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return p.do(req, out)
}

func (p *Pairer) postJSON(ctx context.Context, path, bearer string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+path, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	status, err := p.do(req, out)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("unexpected status %d", status)
	}
	return nil
}

// do sends the request and decodes a JSON body into out when there is one.
func (p *Pairer) do(req *http.Request, out any) (int, error) {
	req.Header.Set("User-Agent", "wrekin/"+p.AgentVersion)
	res, err := p.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if out != nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, fmt.Errorf("status %d with a non-JSON body", res.StatusCode)
		}
	}
	return res.StatusCode, nil
}

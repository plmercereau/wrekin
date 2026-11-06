// Package identity manages the node's Ed25519 key pair and the pairing data
// stored in identity.json. The private key never leaves this file.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotPaired is returned by Load when no identity file exists.
var ErrNotPaired = errors.New("this machine is not paired; run `wrekin auth`")

// AuthMessagePrefix is the domain separator of the relay authentication
// signature (PROTOCOL.md, Handshake).
const AuthMessagePrefix = "wrekin-node-auth-v1"

// UnregisterMessagePrefix is the domain separator of the unregister signature.
const UnregisterMessagePrefix = "wrekin-node-unregister-v1"

// Identity is a node key pair plus the data obtained at pairing time.
type Identity struct {
	NodeID     string
	AccountID  string
	Name       string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
	RelayURL   string
	CreatedAt  time.Time
}

// fileIdentity is the on-disk JSON shape.
type fileIdentity struct {
	NodeID     string    `json:"nodeId"`
	AccountID  string    `json:"accountId"`
	Name       string    `json:"name"`
	PublicKey  string    `json:"publicKey"`
	PrivateKey string    `json:"privateKey"`
	RelayURL   string    `json:"relayUrl"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Generate creates a fresh, unpaired identity for a node called name.
func Generate(name string) (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return &Identity{
		Name:       name,
		PublicKey:  pub,
		PrivateKey: priv,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}, nil
}

// Load reads identity.json. A missing file is ErrNotPaired.
func Load(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotPaired
	}
	if err != nil {
		return nil, fmt.Errorf("read identity: %w", err)
	}
	var f fileIdentity
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	pub, err := base64.StdEncoding.DecodeString(f.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s: invalid public key", path)
	}
	priv, err := base64.StdEncoding.DecodeString(f.PrivateKey)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s: invalid private key", path)
	}
	return &Identity{
		NodeID:     f.NodeID,
		AccountID:  f.AccountID,
		Name:       f.Name,
		PublicKey:  ed25519.PublicKey(pub),
		PrivateKey: ed25519.PrivateKey(priv),
		RelayURL:   f.RelayURL,
		CreatedAt:  f.CreatedAt,
	}, nil
}

// Save writes the identity with mode 0600 through a temporary file and an
// atomic rename, creating the parent directory (0700) when needed.
func (id *Identity) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fileIdentity{
		NodeID:     id.NodeID,
		AccountID:  id.AccountID,
		Name:       id.Name,
		PublicKey:  id.PublicKeyBase64(),
		PrivateKey: base64.StdEncoding.EncodeToString(id.PrivateKey),
		RelayURL:   id.RelayURL,
		CreatedAt:  id.CreatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".identity-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Paired reports whether the relay has assigned a node id to this identity.
func (id *Identity) Paired() bool { return id != nil && id.NodeID != "" }

// Sign returns the base64 Ed25519 signature of the relay authentication
// message for the given node id and nonce (the nonce exactly as received).
func (id *Identity) Sign(nodeID, nonce string) string {
	return id.SignMessage(AuthMessage(nodeID, nonce))
}

// SignMessage signs arbitrary bytes and returns the signature in base64.
func (id *Identity) SignMessage(msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(id.PrivateKey, msg))
}

// PublicKeyBase64 is the raw 32-byte public key in standard base64.
func (id *Identity) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(id.PublicKey)
}

// Fingerprint is a short human-readable digest of the public key: the first
// 16 hex characters of its SHA-256, grouped by four.
func (id *Identity) Fingerprint() string {
	sum := sha256.Sum256(id.PublicKey)
	h := hex.EncodeToString(sum[:])[:16]
	parts := make([]string, 0, 4)
	for i := 0; i < len(h); i += 4 {
		parts = append(parts, h[i:i+4])
	}
	return strings.Join(parts, ":")
}

// AuthMessage builds the bytes signed during the relay handshake.
func AuthMessage(nodeID, nonce string) []byte {
	return []byte(AuthMessagePrefix + "\n" + nodeID + "\n" + nonce)
}

// UnregisterMessage builds the bytes signed when a node unregisters itself.
func UnregisterMessage(nodeID, timestamp string) []byte {
	return []byte(UnregisterMessagePrefix + "\n" + nodeID + "\n" + timestamp)
}

// Verify checks a base64 signature of the authentication message against a
// base64 public key. Used by tests and by the fake relay.
func Verify(pubB64, nodeID, nonce, sigB64 string) bool {
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), AuthMessage(nodeID, nonce), sig)
}

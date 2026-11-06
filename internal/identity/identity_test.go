package identity

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestGenerateSaveLoadRoundTrip(t *testing.T) {
	id, err := Generate("box")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(id.PublicKey) != ed25519.PublicKeySize || len(id.PrivateKey) != ed25519.PrivateKeySize {
		t.Fatalf("unexpected key sizes %d/%d", len(id.PublicKey), len(id.PrivateKey))
	}
	if id.Paired() {
		t.Fatal("a fresh identity must not be paired")
	}
	id.NodeID = "node_01J"
	id.AccountID = "usr_1"
	id.RelayURL = "wss://wrekin.platyplus.io/connect"

	path := filepath.Join(t.TempDir(), "nested", "identity.json")
	if err := id.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("identity file mode = %o, want 0600", mode)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.NodeID != "node_01J" || got.AccountID != "usr_1" || got.Name != "box" || got.RelayURL != id.RelayURL {
		t.Errorf("fields differ after round trip: %+v", got)
	}
	if !got.PublicKey.Equal(id.PublicKey) || !got.PrivateKey.Equal(id.PrivateKey) {
		t.Error("keys differ after round trip")
	}
	if !got.CreatedAt.Equal(id.CreatedAt) {
		t.Errorf("createdAt %v != %v", got.CreatedAt, id.CreatedAt)
	}
	if !got.Paired() {
		t.Error("loaded identity with a node id must be paired")
	}
	// Saving again overwrites atomically and leaves no temporary file behind.
	if err := got.Save(path); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("expected only identity.json in the directory, got %d entries", len(entries))
	}
}

func TestSignVerifiesWithProtocolMessage(t *testing.T) {
	id, _ := Generate("box")
	nonce := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	sigB64 := id.Sign("node_42", nonce)
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("signature size %d", len(sig))
	}
	msg := []byte("wrekin-node-auth-v1\nnode_42\n" + nonce)
	if !ed25519.Verify(id.PublicKey, msg, sig) {
		t.Fatal("signature does not verify over the protocol message")
	}
	if !Verify(id.PublicKeyBase64(), "node_42", nonce, sigB64) {
		t.Fatal("Verify rejected a valid signature")
	}
}

func TestVerifyRejectsTamperedNonce(t *testing.T) {
	id, _ := Generate("box")
	sig := id.Sign("node_42", "nonce-a")
	if Verify(id.PublicKeyBase64(), "node_42", "nonce-b", sig) {
		t.Error("tampered nonce accepted")
	}
	if Verify(id.PublicKeyBase64(), "node_43", "nonce-a", sig) {
		t.Error("tampered node id accepted")
	}
	if Verify("not base64!", "node_42", "nonce-a", sig) {
		t.Error("garbage public key accepted")
	}
	if Verify(id.PublicKeyBase64(), "node_42", "nonce-a", "AAAA") {
		t.Error("short signature accepted")
	}
}

func TestLoadMissingIsErrNotPaired(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "identity.json"))
	if !errors.Is(err, ErrNotPaired) {
		t.Fatalf("err = %v, want ErrNotPaired", err)
	}
}

func TestFingerprintFormat(t *testing.T) {
	id, _ := Generate("box")
	fp := id.Fingerprint()
	if !regexp.MustCompile(`^[0-9a-f]{4}(:[0-9a-f]{4}){3}$`).MatchString(fp) {
		t.Errorf("fingerprint %q has the wrong shape", fp)
	}
	if id.CreatedAt.IsZero() || id.CreatedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("createdAt not set sensibly: %v", id.CreatedAt)
	}
}

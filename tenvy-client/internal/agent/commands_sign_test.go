package agent

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

func TestVerifyCommandSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	secret := "test-secret"

	a := &Agent{
		commandPublicKey: pubHex,
		commandSecret:    secret,
	}

	cmd := protocol.Command{
		ID:        "cmd-1",
		Name:      "ping",
		Payload:   []byte(`{"message":"hi"}`),
		CreatedAt: "2026-01-13T00:00:00Z",
	}

	data := strings.Join([]string{
		cmd.ID,
		cmd.Name,
		string(cmd.Payload),
		cmd.CreatedAt,
	}, "|")

	sig := ed25519.Sign(priv, []byte(data))
	cmd.Signature = "ed25519:" + hex.EncodeToString(sig)
	if !a.verifyCommandSignature(cmd) {
		t.Errorf("Ed25519 signature verification failed")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	cmd.Signature = "hmac:" + hex.EncodeToString(mac.Sum(nil))
	if !a.verifyCommandSignature(cmd) {
		t.Errorf("HMAC signature verification failed")
	}

	cmd.Signature = hex.EncodeToString(mac.Sum(nil))
	if !a.verifyCommandSignature(cmd) {
		t.Errorf("Legacy HMAC signature verification failed")
	}

	cmd.Signature = "ed25519:" + hex.EncodeToString(make([]byte, 64))
	if a.verifyCommandSignature(cmd) {
		t.Errorf("Invalid Ed25519 signature should have failed")
	}

	cmd.Signature = "hmac:" + hex.EncodeToString(make([]byte, 32))
	if a.verifyCommandSignature(cmd) {
		t.Errorf("Invalid HMAC signature should have failed")
	}
}

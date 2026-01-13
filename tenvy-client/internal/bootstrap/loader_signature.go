package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type loaderSignature struct {
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey,omitempty"`
	Signature string `json:"signature"`
}

func NewLoaderSignatureVerifier() LoaderSignatureVerifier {
	return LoaderSignatureVerifierFunc(func(ctx context.Context, loaderPath string, metadata LoaderMetadata) error {
		parsed, err := parseLoaderSignature(metadata.Signature)
		if err != nil {
			return err
		}
		if parsed == nil {
			return nil
		}
		digest, err := computeFileSHA256(loaderPath)
		if err != nil {
			return fmt.Errorf("compute loader digest: %w", err)
		}
		switch parsed.Algorithm {
		case "", "sha256":
			if !strings.EqualFold(digest, parsed.Signature) {
				return fmt.Errorf("loader signature mismatch: expected %s, got %s", parsed.Signature, digest)
			}
			return nil
		case "ed25519":
			publicKeyBytes, err := hex.DecodeString(strings.TrimSpace(parsed.PublicKey))
			if err != nil {
				return fmt.Errorf("loader signature: invalid public key: %w", err)
			}
			if len(publicKeyBytes) != ed25519.PublicKeySize {
				return fmt.Errorf("loader signature: unexpected public key length %d", len(publicKeyBytes))
			}
			signatureBytes, err := hex.DecodeString(strings.TrimSpace(parsed.Signature))
			if err != nil {
				return fmt.Errorf("loader signature: invalid signature encoding: %w", err)
			}
			if len(signatureBytes) != ed25519.SignatureSize {
				return fmt.Errorf("loader signature: unexpected signature length %d", len(signatureBytes))
			}
			if !ed25519.Verify(ed25519.PublicKey(publicKeyBytes), []byte(strings.ToLower(digest)), signatureBytes) {
				return errors.New("loader signature verification failed")
			}
			return nil
		default:
			return fmt.Errorf("unsupported loader signature algorithm: %s", parsed.Algorithm)
		}
	})
}

func computeFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func parseLoaderSignature(raw string) (*loaderSignature, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var payload loaderSignature
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return nil, fmt.Errorf("decode loader signature: %w", err)
		}
		payload.Algorithm = strings.ToLower(strings.TrimSpace(payload.Algorithm))
		payload.Signature = strings.TrimSpace(payload.Signature)
		payload.PublicKey = strings.TrimSpace(payload.PublicKey)
	} else {
		parts := strings.Split(trimmed, ":")
		switch len(parts) {
		case 1:
			payload = loaderSignature{Algorithm: "sha256", Signature: strings.TrimSpace(parts[0])}
		case 2:
			payload = loaderSignature{Algorithm: strings.ToLower(strings.TrimSpace(parts[0])), Signature: strings.TrimSpace(parts[1])}
		case 3:
			payload = loaderSignature{Algorithm: strings.ToLower(strings.TrimSpace(parts[0])), PublicKey: strings.TrimSpace(parts[1]), Signature: strings.TrimSpace(parts[2])}
		default:
			return nil, errors.New("invalid loader signature format")
		}
	}
	if payload.Signature == "" {
		return nil, errors.New("loader signature missing signature value")
	}
	if payload.Algorithm == "ed25519" && strings.TrimSpace(payload.PublicKey) == "" {
		return nil, errors.New("loader signature public key required for ed25519")
	}
	return &payload, nil
}

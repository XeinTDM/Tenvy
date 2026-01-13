package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

const manifestFileName = "manifest.json"

type Manager struct {
	root       string
	logger     *log.Logger
	verifyMu   sync.RWMutex
	verifyOpt  manifest.VerifyOptions
	stageMu    sync.Mutex
	registryMu sync.RWMutex
	registry   map[string]manifest.ManifestDescriptor
}

func NewManager(root string, logger *log.Logger, verify manifest.VerifyOptions) (*Manager, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("plugin root directory not provided")
	}
	if logger == nil {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	manager := &Manager{root: root, logger: logger}
	manager.UpdateVerification(verify)
	return manager, nil
}

func (m *Manager) Snapshot() *manifest.SyncPayload {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		m.logger.Printf("plugin scan failed: %v", err)
		return nil
	}

	now := time.Now().UTC().UnixMilli()
	payload := manifest.SyncPayload{Installations: make([]manifest.InstallationTelemetry, 0, len(entries))}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginDir := filepath.Join(m.root, entry.Name())
		manifestPath := filepath.Join(pluginDir, manifestFileName)

		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			if status := loadInstallationStatus(pluginDir); status != nil {
				telemetry := manifest.InstallationTelemetry{
					PluginID:  status.PluginID(entry.Name()),
					Version:   status.Version,
					Status:    status.Status,
					Timestamp: &now,
				}
				if status.Error != "" {
					telemetry.Error = status.Error
				}
				if ts := status.Timestamp; ts != nil {
					telemetry.Timestamp = ts
				}
				payload.Installations = append(payload.Installations, telemetry)
			} else if !errors.Is(err, fs.ErrNotExist) {
				m.logger.Printf("plugin %s missing manifest: %v", entry.Name(), err)
			}
			continue
		}

		var mf manifest.Manifest
		if err := json.Unmarshal(manifestData, &mf); err != nil {
			m.logger.Printf("plugin %s manifest parse failed: %v", entry.Name(), err)
			continue
		}
		if err := mf.Validate(); err != nil {
			m.logger.Printf("plugin %s manifest invalid: %v", mf.ID, err)
			continue
		}

		installation := manifest.InstallationTelemetry{
			PluginID:  mf.ID,
			Version:   mf.Version,
			Status:    manifest.InstallInstalled,
			Timestamp: &now,
		}

		if m.hasRegistry() {
			if descriptor := m.registryDescriptor(mf.ID); descriptor != nil {
				approvedVersion := strings.TrimSpace(descriptor.Version)
				approvedAt := strings.TrimSpace(descriptor.ApprovedAt)
				currentVersion := strings.TrimSpace(mf.Version)

				switch {
				case approvedVersion == "":
					installation.Status = manifest.InstallBlocked
					installation.Error = "registry: missing approved version"
					installation.Timestamp = &now
					payload.Installations = append(payload.Installations, installation)
					continue
				case !strings.EqualFold(approvedVersion, currentVersion):
					installation.Status = manifest.InstallBlocked
					installation.Error = fmt.Sprintf(
						"registry: version %s not approved (expected %s)",
						currentVersion,
						approvedVersion,
					)
					installation.Timestamp = &now
					payload.Installations = append(payload.Installations, installation)
					continue
				case approvedAt == "":
					installation.Status = manifest.InstallBlocked
					installation.Error = "registry: awaiting approval"
					installation.Timestamp = &now
					payload.Installations = append(payload.Installations, installation)
					continue
				}
			} else {
				installation.Status = manifest.InstallBlocked
				installation.Error = "registry: plugin not approved"
				installation.Timestamp = &now
				payload.Installations = append(payload.Installations, installation)
				continue
			}
		}

		verificationResult, verifyErr := manifest.VerifySignature(mf, m.verificationOptions())
		if verifyErr != nil {
			installation.Status = manifest.InstallBlocked
			installation.Error = fmt.Sprintf("signature: %s", signatureErrorMessage(verifyErr))
			installation.Timestamp = &now
			payload.Installations = append(payload.Installations, installation)
			continue
		}

		if verificationResult == nil || !verificationResult.Trusted {
			installation.Status = manifest.InstallBlocked
			installation.Error = fmt.Sprintf("signature: %s", signatureUntrustedReason(mf, verificationResult))
			installation.Timestamp = &now
			payload.Installations = append(payload.Installations, installation)
			continue
		}

		artifactRel := filepath.Clean(mf.Package.Artifact)
		if strings.HasPrefix(artifactRel, "..") {
			installation.Status = manifest.InstallError
			installation.Error = "artifact path escapes plugin directory"
			installation.Timestamp = &now
			payload.Installations = append(payload.Installations, installation)
			continue
		}

		artifactPath := filepath.Join(pluginDir, artifactRel)
		info, statErr := os.Stat(artifactPath)
		switch {
		case statErr == nil && !info.IsDir():
			hash, hashErr := fileHash(artifactPath)
			if hashErr != nil {
				installation.Status = manifest.InstallError
				installation.Error = fmt.Sprintf("hash: %v", hashErr)
			} else {
				installation.Hash = hash
				if mf.Package.Hash != "" && !strings.EqualFold(mf.Package.Hash, hash) {
					installation.Status = manifest.InstallError
					installation.Error = "hash mismatch"
				} else {
					installation.Status = manifest.InstallInstalled
				}
			}
		case errors.Is(statErr, fs.ErrNotExist):
			installation.Status = manifest.InstallError
			installation.Error = "artifact missing"
		case statErr != nil:
			installation.Status = manifest.InstallError
			installation.Error = statErr.Error()
		default:
			installation.Status = manifest.InstallError
			installation.Error = "artifact is a directory"
		}

		installation.Timestamp = &now

		if status := loadInstallationStatus(pluginDir); status != nil {
			if status.Status != "" {
				installation.Status = status.Status
			}
			if status.Error != "" {
				installation.Error = status.Error
			}
			if ts := status.Timestamp; ts != nil {
				installation.Timestamp = ts
			}
			if version := status.Version; version != "" {
				installation.Version = version
			}
		}

		payload.Installations = append(payload.Installations, installation)
	}

	if len(payload.Installations) == 0 {
		return nil
	}

	return &payload
}

func (m *Manager) registryDescriptor(pluginID string) *manifest.ManifestDescriptor {
	if m == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(pluginID))
	if normalized == "" {
		return nil
	}
	m.registryMu.RLock()
	defer m.registryMu.RUnlock()
	if len(m.registry) == 0 {
		return nil
	}
	descriptor, ok := m.registry[normalized]
	if !ok {
		return nil
	}
	copy := descriptor
	return &copy
}

func (m *Manager) hasRegistry() bool {
	if m == nil {
		return false
	}
	m.registryMu.RLock()
	defer m.registryMu.RUnlock()
	return len(m.registry) > 0
}

func (m *Manager) UpdateRegistry(list *manifest.ManifestList) {
	if m == nil {
		return
	}
	m.registryMu.Lock()
	defer m.registryMu.Unlock()

	if list == nil || len(list.Manifests) == 0 {
		m.registry = nil
		return
	}

	if m.registry == nil {
		m.registry = make(map[string]manifest.ManifestDescriptor, len(list.Manifests))
	} else {
		for key := range m.registry {
			delete(m.registry, key)
		}
	}

	for _, descriptor := range list.Manifests {
		id := strings.ToLower(strings.TrimSpace(descriptor.PluginID))
		if id == "" {
			continue
		}
		m.registry[id] = descriptor
	}
}

func (m *Manager) Root() string {
	if m == nil {
		return ""
	}
	return m.root
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum), nil
}

func (m *Manager) UpdateVerification(opts manifest.VerifyOptions) {
	m.verifyMu.Lock()
	m.verifyOpt = cloneVerifyOptions(opts)
	m.verifyMu.Unlock()
}

func (m *Manager) verificationOptions() manifest.VerifyOptions {
	m.verifyMu.RLock()
	defer m.verifyMu.RUnlock()
	return cloneVerifyOptions(m.verifyOpt)
}

func cloneVerifyOptions(opts manifest.VerifyOptions) manifest.VerifyOptions {
	clone := opts
	if len(opts.SHA256AllowList) > 0 {
		clone.SHA256AllowList = append([]string(nil), opts.SHA256AllowList...)
	}
	if len(opts.Ed25519PublicKeys) > 0 {
		clone.Ed25519PublicKeys = make(map[string]ed25519.PublicKey, len(opts.Ed25519PublicKeys))
		for keyID, key := range opts.Ed25519PublicKeys {
			clone.Ed25519PublicKeys[keyID] = append(ed25519.PublicKey(nil), key...)
		}
	}
	return clone
}

func signatureErrorMessage(err error) string {
	switch {
	case errors.Is(err, manifest.ErrUnsignedPlugin):
		return "unsigned plugin"
	case errors.Is(err, manifest.ErrSignatureMismatch):
		return "hash mismatch"
	case errors.Is(err, manifest.ErrHashNotAllowed):
		return "hash not allowed"
	case errors.Is(err, manifest.ErrUntrustedSigner):
		return "untrusted signer"
	case errors.Is(err, manifest.ErrInvalidSignature):
		return "invalid signature"
	case errors.Is(err, manifest.ErrSignatureExpired):
		return "signature expired"
	case errors.Is(err, manifest.ErrSignatureNotYetValid):
		return "signature timestamp in future"
	default:
		return err.Error()
	}
}

func signatureUntrustedReason(mf manifest.Manifest, result *manifest.VerificationResult) string {
	if result == nil {
		return "signature not trusted"
	}

	switch result.SignatureType {
	case manifest.SignatureSHA256:
		return "hash not trusted"
	case manifest.SignatureEd25519:
		if strings.TrimSpace(result.Signer) != "" {
			return fmt.Sprintf("untrusted signer %s", result.Signer)
		}
		if strings.TrimSpace(result.PublicKey) != "" {
			return fmt.Sprintf("untrusted key %s", result.PublicKey)
		}
		if strings.TrimSpace(mf.Distribution.SignatureSigner) != "" {
			return fmt.Sprintf("untrusted signer %s", mf.Distribution.SignatureSigner)
		}
		return "untrusted signer"
	default:
		return "untrusted signature"
	}
}

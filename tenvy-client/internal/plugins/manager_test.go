package plugins_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plugins "github.com/rootbay/tenvy-client/internal/plugins"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestSnapshotSkipsManifestWithUnsupportedSignature(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "unsigned")
	artifactPath := filepath.Join(pluginDir, "plugin.dll")

	manifestPath := filepath.Join(pluginDir, "manifest.json")
	writeFile(t, manifestPath, []byte(`{
                "id": "unsigned",
                "name": "Unsigned",
                "version": "1.0.0",
                "entry": "plugin.dll",
                "repositoryUrl": "https://github.com/rootbay/unsigned",
                "license": { "spdxId": "MIT" },
                "requirements": {},
                "distribution": {"defaultMode": "manual", "autoUpdate": false, "signature": "none"},
                "package": {"artifact": "plugin.dll"}
        }`))
	writeFile(t, artifactPath, []byte("payload"))

	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), manifest.VerifyOptions{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if snapshot := manager.Snapshot(); snapshot != nil {
		t.Fatalf("expected snapshot to be nil for unsupported signature, got %#v", snapshot)
	}
}

func TestSnapshotAllowsTrustedSignature(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "trusted")
	artifactPath := filepath.Join(pluginDir, "plugin.bin")

	payload := make([]byte, 128)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}
	writeFile(t, artifactPath, payload)

	hash := sha256SumHex(payload)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signature := ed25519.Sign(priv, []byte(hash))

	manifestJSON := fmt.Sprintf(`{
                "id": "trusted",
                "name": "Trusted",
                "version": "1.0.0",
                "entry": "plugin.bin",
                "repositoryUrl": "https://github.com/rootbay/trusted",
                "license": { "spdxId": "MIT" },
                "requirements": {},
                "distribution": {
                        "defaultMode": "manual",
                        "autoUpdate": false,
                        "signature": "ed25519",
                        "signatureHash": "%s",
                        "signatureSigner": "primary",
                        "signatureValue": "%s"
                },
                "package": {"artifact": "plugin.bin", "hash": "%s"}
        }`, hash, hex.EncodeToString(signature), hash)
	writeFile(t, filepath.Join(pluginDir, "manifest.json"), []byte(manifestJSON))

	opts := manifest.VerifyOptions{
		Ed25519PublicKeys: map[string]ed25519.PublicKey{"primary": pub},
	}
	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected one installation, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallInstalled {
		t.Fatalf("expected status installed, got %s", install.Status)
	}
	if !strings.EqualFold(install.Hash, hash) {
		t.Fatalf("expected hash %s, got %s", hash, install.Hash)
	}
	if install.Timestamp == nil || *install.Timestamp == 0 {
		t.Fatalf("expected timestamp to be populated, got %#v", install.Timestamp)
	}
}

func TestSnapshotHonorsRegistryApproval(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "approved")
	artifactPath := filepath.Join(pluginDir, "plugin.bin")

	payload := []byte("payload")
	writeFile(t, artifactPath, payload)

	hash := sha256SumHex(payload)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signature := ed25519.Sign(priv, []byte(hash))

	manifestJSON := fmt.Sprintf(`{
                "id": "approved",
                "name": "Approved",
                "version": "1.0.0",
                "entry": "plugin.bin",
                "repositoryUrl": "https://github.com/rootbay/approved",
                "license": { "spdxId": "MIT" },
                "requirements": {},
                "distribution": {
                        "defaultMode": "manual",
                        "autoUpdate": false,
                        "signature": "ed25519",
                        "signatureHash": "%s",
                        "signatureSigner": "primary",
                        "signatureValue": "%s"
                },
                "package": {"artifact": "plugin.bin", "hash": "%s"}
        }`, hash, hex.EncodeToString(signature), hash)
	writeFile(t, filepath.Join(pluginDir, "manifest.json"), []byte(manifestJSON))

	opts := manifest.VerifyOptions{Ed25519PublicKeys: map[string]ed25519.PublicKey{"primary": pub}}
	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	manager.UpdateRegistry(&manifest.ManifestList{
		Version: "1",
		Manifests: []manifest.ManifestDescriptor{{
			PluginID:   "approved",
			Version:    "1.0.0",
			ApprovedAt: time.Now().UTC().Format(time.RFC3339),
		}},
	})

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected one installation, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallInstalled {
		t.Fatalf("expected approved plugin to be installed, got %s", install.Status)
	}
}

func TestSnapshotBlocksUnapprovedRegistryPlugin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "pending")
	artifactPath := filepath.Join(pluginDir, "plugin.bin")
	payload := []byte("payload")
	writeFile(t, artifactPath, payload)

	hash := sha256SumHex(payload)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signature := ed25519.Sign(priv, []byte(hash))
	manifestJSON := fmt.Sprintf(`{
                "id": "pending",
                "name": "Pending",
                "version": "1.0.0",
                "entry": "plugin.bin",
                "repositoryUrl": "https://github.com/rootbay/pending",
                "license": { "spdxId": "MIT" },
                "requirements": {},
                "distribution": {
                        "defaultMode": "manual",
                        "autoUpdate": false,
                        "signature": "ed25519",
                        "signatureHash": "%s",
                        "signatureSigner": "primary",
                        "signatureValue": "%s"
                },
                "package": {"artifact": "plugin.bin", "hash": "%s"}
        }`, hash, hex.EncodeToString(signature), hash)
	writeFile(t, filepath.Join(pluginDir, "manifest.json"), []byte(manifestJSON))

	opts := manifest.VerifyOptions{Ed25519PublicKeys: map[string]ed25519.PublicKey{"primary": pub}}
	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	manager.UpdateRegistry(&manifest.ManifestList{
		Version: "1",
		Manifests: []manifest.ManifestDescriptor{{
			PluginID: "pending",
			Version:  "1.0.0",
		}},
	})

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected one installation, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallBlocked {
		t.Fatalf("expected pending plugin to be blocked, got %s", install.Status)
	}
	if install.Error == "" || !strings.Contains(install.Error, "awaiting approval") {
		t.Fatalf("expected awaiting approval error, got %q", install.Error)
	}
}

func TestSnapshotBlocksInvalidSignature(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "invalid")
	artifactPath := filepath.Join(pluginDir, "plugin.bin")

	payload := []byte("data")
	writeFile(t, artifactPath, payload)
	hash := sha256SumHex(payload)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	manifestJSON := fmt.Sprintf(`{
                "id": "invalid",
                "name": "Invalid",
                "version": "1.0.0",
                "entry": "plugin.bin",
                "repositoryUrl": "https://github.com/rootbay/invalid",
                "license": { "spdxId": "MIT" },
                "requirements": {},
                "distribution": {
                        "defaultMode": "manual",
                        "autoUpdate": false,
                        "signature": "ed25519",
                        "signatureHash": "%s",
                        "signatureSigner": "primary",
                        "signatureValue": "%s"
                },
                "package": {"artifact": "plugin.bin", "hash": "%s"}
        }`, hash, strings.Repeat("00", ed25519.SignatureSize), hash)
	writeFile(t, filepath.Join(pluginDir, "manifest.json"), []byte(manifestJSON))

	opts := manifest.VerifyOptions{
		Ed25519PublicKeys: map[string]ed25519.PublicKey{"primary": pub},
	}
	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected one installation, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallBlocked {
		t.Fatalf("expected blocked status, got %s", install.Status)
	}
	if install.Error == "" || !strings.Contains(install.Error, "invalid") {
		t.Fatalf("expected invalid signature error, got %q", install.Error)
	}
	if install.Timestamp == nil || *install.Timestamp == 0 {
		t.Fatalf("expected timestamp to be populated, got %#v", install.Timestamp)
	}
}

func TestSnapshotAppliesRecordedStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "remote-desktop-engine")
	artifactPath := filepath.Join(pluginDir, "engine.bin")

	payload := []byte("payload")
	writeFile(t, artifactPath, payload)
	hash := sha256.Sum256(payload)
	hashHex := fmt.Sprintf("%x", hash[:])

	writeFile(t, filepath.Join(pluginDir, "manifest.json"), []byte(fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "1.0.0",
                "entry": "engine.bin",
                "repositoryUrl": "https://github.com/rootbay/tenvy",
                "license": { "spdxId": "MIT" },
                "requirements": {},
                "distribution": {"defaultMode": "automatic", "autoUpdate": true, "signature": "sha256", "signatureHash": "%[1]s"},
                "package": {"artifact": "engine.bin", "hash": "%[1]s"}
        }`, hashHex)))

	opts := manifest.VerifyOptions{SHA256AllowList: []string{hashHex}}
	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := plugins.RecordInstallStatus(manager, "remote-desktop-engine", "1.0.0", manifest.InstallError, "download failed"); err != nil {
		t.Fatalf("record status: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected single installation, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallError {
		t.Fatalf("expected failed status, got %s", install.Status)
	}
	if install.Error != "download failed" {
		t.Fatalf("expected error message propagated, got %q", install.Error)
	}
	if install.Timestamp == nil || *install.Timestamp == 0 {
		t.Fatalf("expected timestamp from status file, got %#v", install.Timestamp)
	}
}

func TestSnapshotWithoutManifestUsesStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), manifest.VerifyOptions{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := plugins.RecordInstallStatus(manager, "remote-desktop-engine", "1.2.3", manifest.InstallError, "network error"); err != nil {
		t.Fatalf("record status: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected snapshot entry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.PluginID != "remote-desktop-engine" {
		t.Fatalf("unexpected plugin id %s", install.PluginID)
	}
	if install.Version != "1.2.3" {
		t.Fatalf("unexpected version %s", install.Version)
	}
	if install.Status != manifest.InstallError {
		t.Fatalf("expected failed status, got %s", install.Status)
	}
	if install.Error != "network error" {
		t.Fatalf("expected error propagated, got %q", install.Error)
	}
	if install.Timestamp == nil || *install.Timestamp == 0 {
		t.Fatalf("expected timestamp from status record, got %#v", install.Timestamp)
	}
}

func TestSnapshotEmitsDocumentedStatuses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	installedDir := filepath.Join(root, "installed-plugin")
	installedArtifact := filepath.Join(installedDir, "plugin.bin")
	installedPayload := []byte("installed artifact")
	writeFile(t, installedArtifact, installedPayload)
	installedHash := sha256SumHex(installedPayload)
	installedManifest := fmt.Sprintf(`{
                "id": "installed-plugin",
                "name": "Installed Plugin",
                "version": "1.0.0",
                "entry": "plugin.bin",
                "requirements": {},
                "distribution": {"defaultMode": "manual", "autoUpdate": false, "signature": "sha256", "signatureHash": "%[1]s"},
                "package": {"artifact": "plugin.bin", "hash": "%[1]s"}
        }`, installedHash)
	writeFile(t, filepath.Join(installedDir, "manifest.json"), []byte(installedManifest))

	blockedDir := filepath.Join(root, "blocked-plugin")
	blockedArtifact := filepath.Join(blockedDir, "plugin.bin")
	blockedPayload := []byte("blocked artifact")
	writeFile(t, blockedArtifact, blockedPayload)
	blockedHash := sha256SumHex(blockedPayload)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	blockedManifest := fmt.Sprintf(`{
                "id": "blocked-plugin",
                "name": "Blocked Plugin",
                "version": "1.0.0",
                "entry": "plugin.bin",
                "requirements": {},
                "distribution": {
                        "defaultMode": "manual",
                        "autoUpdate": false,
                        "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "primary", "signatureValue": "%[2]s"
                },
                "package": {"artifact": "plugin.bin", "hash": "%[1]s"}
        }`, blockedHash, strings.Repeat("00", ed25519.SignatureSize))
	writeFile(t, filepath.Join(blockedDir, "manifest.json"), []byte(blockedManifest))

	errorDir := filepath.Join(root, "error-plugin")
	errorHash := sha256SumHex([]byte("missing artifact payload"))
	errorManifest := fmt.Sprintf(`{
                "id": "error-plugin",
                "name": "Error Plugin",
                "version": "1.0.0",
                "entry": "missing.bin",
                "requirements": {},
                "distribution": {"defaultMode": "manual", "autoUpdate": false, "signature": "sha256", "signatureHash": "%[1]s"},
                "package": {"artifact": "missing.bin", "hash": "%[1]s"}
        }`, errorHash)
	writeFile(t, filepath.Join(errorDir, "manifest.json"), []byte(errorManifest))

	opts := manifest.VerifyOptions{
		SHA256AllowList:   []string{installedHash, errorHash},
		Ed25519PublicKeys: map[string]ed25519.PublicKey{"primary": pub},
	}
	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := plugins.RecordInstallStatus(manager, "disabled-plugin", "0.0.1", manifest.InstallDisabled, "disabled by policy"); err != nil {
		t.Fatalf("record disabled status: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil {
		t.Fatal("expected snapshot payload")
	}
	if len(snapshot.Installations) != 4 {
		t.Fatalf("expected 4 installations, got %d", len(snapshot.Installations))
	}

	allowed := map[manifest.PluginInstallStatus]struct{}{
		manifest.InstallInstalled: {},
		manifest.InstallBlocked:   {},
		manifest.InstallError:     {},
		manifest.InstallDisabled:  {},
	}
	seen := make(map[manifest.PluginInstallStatus]bool)
	for _, install := range snapshot.Installations {
		if _, ok := allowed[install.Status]; !ok {
			t.Fatalf("unexpected status emitted: %s", install.Status)
		}
		seen[install.Status] = true
	}
	for status := range allowed {
		if !seen[status] {
			t.Fatalf("expected status %s to be included", status)
		}
	}
}

func TestRecordInstallStatusStoresEpochMillis(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), manifest.VerifyOptions{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := plugins.RecordInstallStatus(manager, "example", "1.0.0", manifest.InstallError, "oops"); err != nil {
		t.Fatalf("record status: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "example", ".status.json"))
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal status file: %v", err)
	}
	raw, ok := payload["timestamp"]
	if !ok {
		t.Fatalf("expected timestamp field in status payload: %#v", payload)
	}
	millis, ok := raw.(float64)
	if !ok {
		t.Fatalf("expected timestamp to be numeric, got %T (%v)", raw, raw)
	}
	if millis <= 0 {
		t.Fatalf("expected positive timestamp, got %f", millis)
	}
}

func TestSnapshotConvertsLegacyTimestamp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manager, err := plugins.NewManager(root, nil, log.New(io.Discard, "", 0), manifest.VerifyOptions{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	pluginDir := filepath.Join(root, "legacy-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	legacy := time.Now().UTC().Truncate(time.Millisecond)
	legacyStatus := map[string]any{
		"pluginId":  "legacy-plugin",
		"version":   "9.9.9",
		"status":    string(manifest.InstallInstalled),
		"timestamp": legacy.Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(legacyStatus)
	if err != nil {
		t.Fatalf("marshal legacy status: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, ".status.json"), data, 0o644); err != nil {
		t.Fatalf("write status file: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected legacy status in snapshot, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Timestamp == nil {
		t.Fatalf("expected timestamp parsed from legacy status, got nil")
	}
	if *install.Timestamp != legacy.UnixMilli() {
		t.Fatalf("expected timestamp %d, got %d", legacy.UnixMilli(), *install.Timestamp)
	}
}

func sha256SumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

package plugins_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	plugins "github.com/rootbay/tenvy-client/internal/plugins"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

const (
	releaseSeedHex       = "11e4f334ae8f5dafc590d45ea6465ed65280579656f324cafe4d73109db06936"
	releasePublicKeyHex  = "ea9ceca1c7c7176859b235e095cbca9b5755746b741865cab5458d6f0e754cc2"
	releaseSigner        = "release"
	releaseSignedAtStamp = "2024-01-01T00:00:00Z"
)

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return decoded
}

func releasePublicKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	return ed25519.PublicKey(decodeHex(t, releasePublicKeyHex))
}

func releaseSignatureFor(t *testing.T, hash string) string {
	t.Helper()
	seed := decodeHex(t, releaseSeedHex)
	privateKey := ed25519.NewKeyFromSeed(seed)
	normalized := strings.ToLower(strings.TrimSpace(hash))
	signature := ed25519.Sign(privateKey, []byte(normalized))
	return hex.EncodeToString(signature)
}

func releaseVerifyOptions(t *testing.T, allowList ...string) manifest.VerifyOptions {
	t.Helper()
	opts := manifest.VerifyOptions{
		Ed25519PublicKeys: map[string]ed25519.PublicKey{
			releaseSigner: releasePublicKey(t),
		},
	}
	if len(allowList) > 0 {
		opts.SHA256AllowList = append([]string(nil), allowList...)
	}
	return opts
}

func buildDescriptor(manifestJSON, version, artifactHash string, briefing manifest.ManifestBriefing) manifest.ManifestDescriptor {
	digest := sha256.Sum256([]byte(manifestJSON))
	descriptor := manifest.ManifestDescriptor{
		PluginID:       plugins.RemoteDesktopEnginePluginID,
		Version:        version,
		ManifestDigest: fmt.Sprintf("%x", digest[:]),
		Distribution:   briefing,
	}
	if artifactHash != "" {
		descriptor.ArtifactHash = artifactHash
	}
	return descriptor
}

func TestStageRemoteDesktopEngineSuccess(t *testing.T) {
	t.Parallel()

	artifactData := buildRemoteDesktopArtifact(t, map[string][]byte{
		"remote-desktop-engine/remote-desktop-engine": []byte("engine payload"),
	})
	hash := sha256.Sum256(artifactData)
	hashHex := fmt.Sprintf("%x", hash[:])
	signatureValue := releaseSignatureFor(t, hashHex)

	manifestJSON := fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "9.9.9",
                "entry": "remote-desktop-engine/remote-desktop-engine",
                "repositoryUrl": "https://github.com/rootbay/tenvy-client",
                "license": { "spdxId": "MIT" },
                "requirements": {},
                "distribution": {"defaultMode": "automatic", "autoUpdate": true, "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "%[2]s", "signatureValue": "%[3]s", "signatureTimestamp": "%[4]s"},
                "package": {"artifact": "remote-desktop-engine.zip", "hash": "%[1]s"}
        }`, hashHex, releaseSigner, signatureValue, releaseSignedAtStamp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/artifact") {
			w.Header().Set("Content-Type", "application/octet-stream")
			if _, err := w.Write(artifactData); err != nil {
				t.Fatalf("write artifact: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, manifestJSON); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	opts := releaseVerifyOptions(t)
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	descriptor := buildDescriptor(
		manifestJSON,
		"9.9.9",
		hashHex,
		manifest.ManifestBriefing{DefaultMode: manifest.DeliveryAutomatic, AutoUpdate: true},
	)

	ctx := context.Background()
	result, err := plugins.StageRemoteDesktopEngine(ctx, manager, server.Client(), server.URL, "agent-1", "", "stage-test", manifest.RuntimeFacts{}, descriptor)
	if err != nil {
		t.Fatalf("stage engine: %v", err)
	}
	if !result.Updated {
		t.Fatalf("expected install to be marked updated")
	}
	if strings.TrimSpace(result.EntryPath) == "" {
		t.Fatalf("expected entry path, got empty string")
	}

	entryPayload, err := os.ReadFile(result.EntryPath)
	if err != nil {
		t.Fatalf("read entry payload: %v", err)
	}
	if string(entryPayload) != "engine payload" {
		t.Fatalf("unexpected entry payload %q", string(entryPayload))
	}

	artifactPath := filepath.Join(manager.Root(), plugins.RemoteDesktopEnginePluginID, "remote-desktop-engine.zip")
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact persisted: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected snapshot entry, got %#v", snapshot)
	}
	if snapshot.Installations[0].Status != manifest.InstallInstalled {
		t.Fatalf("expected installed status, got %s", snapshot.Installations[0].Status)
	}

	result2, err := plugins.StageRemoteDesktopEngine(ctx, manager, server.Client(), server.URL, "agent-1", "", "stage-test", manifest.RuntimeFacts{}, descriptor)
	if err != nil {
		t.Fatalf("restage engine: %v", err)
	}
	if result2.Updated {
		t.Fatalf("expected restage to be no-op")
	}
}

func TestStageRemoteDesktopEngineSuccessTarGz(t *testing.T) {
	t.Parallel()

	artifactData := buildTarGzArchive(t, map[string][]byte{
		"remote-desktop-engine/remote-desktop-engine": []byte("engine payload"),
	})
	hash := sha256.Sum256(artifactData)
	hashHex := fmt.Sprintf("%x", hash[:])
	signatureValue := releaseSignatureFor(t, hashHex)

	manifestJSON := fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "9.9.9",
                "entry": "remote-desktop-engine/remote-desktop-engine",
                "repositoryUrl": "https://github.com/rootbay/tenvy-client",
                "license": {"spdxId": "MIT"},
                "requirements": {},
                "distribution": {"defaultMode": "automatic", "autoUpdate": true, "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "%[2]s", "signatureValue": "%[3]s", "signatureTimestamp": "%[4]s"},
                "package": {"artifact": "remote-desktop-engine.tar.gz", "hash": "%[1]s"}
        }`, hashHex, releaseSigner, signatureValue, releaseSignedAtStamp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/artifact") {
			w.Header().Set("Content-Type", "application/octet-stream")
			if _, err := w.Write(artifactData); err != nil {
				t.Fatalf("write artifact: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, manifestJSON); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	opts := releaseVerifyOptions(t)
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	descriptor := buildDescriptor(
		manifestJSON,
		"9.9.9",
		hashHex,
		manifest.ManifestBriefing{DefaultMode: manifest.DeliveryAutomatic, AutoUpdate: true},
	)

	ctx := context.Background()
	result, err := plugins.StageRemoteDesktopEngine(ctx, manager, server.Client(), server.URL, "agent-1", "", "stage-test", manifest.RuntimeFacts{}, descriptor)
	if err != nil {
		t.Fatalf("stage engine: %v", err)
	}
	if !result.Updated {
		t.Fatalf("expected install to be marked updated")
	}
	if strings.TrimSpace(result.EntryPath) == "" {
		t.Fatalf("expected entry path, got empty string")
	}

	entryPayload, err := os.ReadFile(result.EntryPath)
	if err != nil {
		t.Fatalf("read entry payload: %v", err)
	}
	if string(entryPayload) != "engine payload" {
		t.Fatalf("unexpected entry payload %q", string(entryPayload))
	}

	artifactPath := filepath.Join(manager.Root(), plugins.RemoteDesktopEnginePluginID, "remote-desktop-engine.tar.gz")
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact persisted: %v", err)
	}
}

func TestStageRemoteDesktopEngineFailsMalformedTarGz(t *testing.T) {
	t.Parallel()

	artifactData := []byte("not a tar.gz archive")
	hash := sha256.Sum256(artifactData)
	hashHex := fmt.Sprintf("%x", hash[:])
	signatureValue := releaseSignatureFor(t, hashHex)

	manifestJSON := fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "2.2.2",
                "entry": "remote-desktop-engine/remote-desktop-engine",
                "repositoryUrl": "https://github.com/rootbay/tenvy-client",
                "license": {"spdxId": "MIT"},
                "requirements": {},
                "distribution": {"defaultMode": "automatic", "autoUpdate": true, "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "%[2]s", "signatureValue": "%[3]s", "signatureTimestamp": "%[4]s"},
                "package": {"artifact": "remote-desktop-engine.tar.gz", "hash": "%[1]s"}
        }`, hashHex, releaseSigner, signatureValue, releaseSignedAtStamp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/artifact") {
			w.Header().Set("Content-Type", "application/octet-stream")
			if _, err := w.Write(artifactData); err != nil {
				t.Fatalf("write artifact: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, manifestJSON); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	opts := releaseVerifyOptions(t)
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	descriptor := buildDescriptor(
		manifestJSON,
		"2.2.2",
		hashHex,
		manifest.ManifestBriefing{DefaultMode: manifest.DeliveryAutomatic, AutoUpdate: true},
	)

	_, err = plugins.StageRemoteDesktopEngine(context.Background(), manager, server.Client(), server.URL, "agent-1", "", "stage-test", manifest.RuntimeFacts{}, descriptor)
	if err == nil {
		t.Fatal("expected staging to fail")
	}
	if !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("expected artifact failure, got %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected failure snapshot entry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallError {
		t.Fatalf("expected failure status, got %s", install.Status)
	}
	if install.Error == "" || !strings.Contains(install.Error, "artifact") {
		t.Fatalf("expected artifact error message, got %q", install.Error)
	}
}

func TestStageRemoteDesktopEngineRecordsFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	root := t.TempDir()
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), manifest.VerifyOptions{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	descriptor := manifest.ManifestDescriptor{
		PluginID:     plugins.RemoteDesktopEnginePluginID,
		Version:      "0.0.0",
		Distribution: manifest.ManifestBriefing{DefaultMode: manifest.DeliveryAutomatic, AutoUpdate: true},
	}

	_, err = plugins.StageRemoteDesktopEngine(context.Background(), manager, server.Client(), server.URL, "agent-1", "", "stage-test", manifest.RuntimeFacts{}, descriptor)
	if err == nil {
		t.Fatal("expected staging to fail")
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected failure snapshot entry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallError {
		t.Fatalf("expected failure status, got %s", install.Status)
	}
	if !strings.Contains(install.Error, "boom") {
		t.Fatalf("expected controller error message, got %q", install.Error)
	}
}

func TestStageRemoteDesktopEngineRespectsManualPolicyWithoutSignal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), manifest.VerifyOptions{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	descriptor := manifest.ManifestDescriptor{
		PluginID:       plugins.RemoteDesktopEnginePluginID,
		Version:        "1.0.0",
		ManifestDigest: "",
		Distribution: manifest.ManifestBriefing{
			DefaultMode: manifest.DeliveryManual,
			AutoUpdate:  false,
		},
	}

	_, err = plugins.StageRemoteDesktopEngine(
		context.Background(),
		manager,
		server.Client(),
		server.URL,
		"agent-1",
		"",
		"stage-test",
		manifest.RuntimeFacts{},
		descriptor,
	)
	if err == nil {
		t.Fatal("expected manual policy error")
	}
	if !strings.Contains(err.Error(), "automatic staging disabled by policy") {
		t.Fatalf("unexpected error: %v", err)
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected manual policy installation telemetry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallDisabled {
		t.Fatalf("expected disabled status, got %s", install.Status)
	}
	if install.Version != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %q", install.Version)
	}
	if install.PluginID != plugins.RemoteDesktopEnginePluginID {
		t.Fatalf("unexpected plugin id %q", install.PluginID)
	}
}

func TestStageRemoteDesktopEngineAllowsManualWhenRequested(t *testing.T) {
	t.Parallel()

	artifactData := buildRemoteDesktopArtifact(t, map[string][]byte{
		"remote-desktop-engine/remote-desktop-engine": []byte("engine payload"),
	})
	hash := sha256.Sum256(artifactData)
	hashHex := fmt.Sprintf("%x", hash[:])
	signatureValue := releaseSignatureFor(t, hashHex)

	manifestJSON := fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "5.5.5",
                "entry": "remote-desktop-engine/remote-desktop-engine",
                "repositoryUrl": "https://github.com/rootbay/tenvy-client",
                "license": {"spdxId": "MIT"},
                "requirements": {"platforms": ["windows"], "architectures": ["x86_64"]},
                "distribution": {"defaultMode": "manual", "autoUpdate": false, "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "%[2]s", "signatureValue": "%[3]s", "signatureTimestamp": "%[4]s"},
                "package": {"artifact": "remote-desktop-engine.zip", "hash": "%[1]s"}
        }`, hashHex, releaseSigner, signatureValue, releaseSignedAtStamp)

	var artifactServed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/artifact") {
			artifactServed.Store(true)
			w.Header().Set("Content-Type", "application/octet-stream")
			if _, err := w.Write(artifactData); err != nil {
				t.Fatalf("write artifact: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, manifestJSON); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	opts := releaseVerifyOptions(t)
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	descriptor := buildDescriptor(
		manifestJSON,
		"5.5.5",
		hashHex,
		manifest.ManifestBriefing{DefaultMode: manifest.DeliveryManual, AutoUpdate: false},
	)
	descriptor.ManualPushAt = "2024-01-02T03:04:05Z"

	facts := manifest.RuntimeFacts{
		Platform:       "windows",
		Architecture:   "x86_64",
		AgentVersion:   "1.0.0",
		EnabledModules: []string{"remote-desktop"},
	}

	result, err := plugins.StageRemoteDesktopEngine(
		context.Background(),
		manager,
		server.Client(),
		server.URL,
		"agent-1",
		"",
		"stage-test",
		facts,
		descriptor,
	)
	if err != nil {
		t.Fatalf("stage manual plugin: %v", err)
	}
	if !result.Updated {
		t.Fatal("expected staging to update installation")
	}
	if !artifactServed.Load() {
		t.Fatal("expected artifact download to occur")
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected installation telemetry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallInstalled {
		t.Fatalf("expected installed status, got %s", install.Status)
	}
	if install.Version != "5.5.5" {
		t.Fatalf("expected version recorded, got %q", install.Version)
	}
	if install.Hash == "" {
		t.Fatal("expected hash recorded")
	}
}

func TestStageRemoteDesktopEngineBlocksIncompatiblePlatform(t *testing.T) {
	t.Parallel()

	hashHex := strings.Repeat("ab", 32)
	signatureValue := releaseSignatureFor(t, hashHex)
	manifestJSON := fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "1.0.0",
                "entry": "remote-desktop-engine/remote-desktop-engine",
                "repositoryUrl": "https://github.com/rootbay/tenvy-client",
                "license": {"spdxId": "MIT"},
                "requirements": {"platforms": ["windows"]},
                "distribution": {"defaultMode": "manual", "autoUpdate": false, "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "%[2]s", "signatureValue": "%[3]s", "signatureTimestamp": "%[4]s"},
                "package": {"artifact": "remote-desktop-engine.zip", "hash": "%[1]s"}
        }`, hashHex, releaseSigner, signatureValue, releaseSignedAtStamp)

	var artifactRequested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/artifact") {
			artifactRequested.Store(true)
			t.Fatalf("artifact download should not be attempted")
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, manifestJSON); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	opts := releaseVerifyOptions(t)
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	facts := manifest.RuntimeFacts{
		Platform:       "linux",
		Architecture:   "x86_64",
		AgentVersion:   "1.0.0",
		EnabledModules: []string{"remote-desktop"},
	}

	descriptor := buildDescriptor(
		manifestJSON,
		"1.0.0",
		hashHex,
		manifest.ManifestBriefing{DefaultMode: manifest.DeliveryAutomatic, AutoUpdate: true},
	)

	_, err = plugins.StageRemoteDesktopEngine(context.Background(), manager, server.Client(), server.URL, "agent-1", "", "stage-test", facts, descriptor)
	if err == nil {
		t.Fatal("expected staging to be blocked")
	}
	if !strings.Contains(err.Error(), "platform") {
		t.Fatalf("expected platform error, got %v", err)
	}
	if artifactRequested.Load() {
		t.Fatal("expected no artifact download attempts")
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected snapshot entry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallBlocked {
		t.Fatalf("expected blocked status, got %s", install.Status)
	}
	if !strings.Contains(install.Error, "platform") {
		t.Fatalf("expected platform message, got %q", install.Error)
	}
}

func TestStageRemoteDesktopEngineBlocksIncompatibleArchitecture(t *testing.T) {
	t.Parallel()

	hashHex := strings.Repeat("cd", 32)
	signatureValue := releaseSignatureFor(t, hashHex)
	manifestJSON := fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "1.0.0",
                "entry": "remote-desktop-engine/remote-desktop-engine",
                "repositoryUrl": "https://github.com/rootbay/tenvy-client",
                "license": {"spdxId": "MIT"},
                "requirements": {"architectures": ["arm64"]},
                "distribution": {"defaultMode": "manual", "autoUpdate": false, "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "%[2]s", "signatureValue": "%[3]s", "signatureTimestamp": "%[4]s"},
                "package": {"artifact": "remote-desktop-engine.zip", "hash": "%[1]s"}
        }`, hashHex, releaseSigner, signatureValue, releaseSignedAtStamp)

	var artifactRequested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/artifact") {
			artifactRequested.Store(true)
			t.Fatalf("artifact download should not be attempted")
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, manifestJSON); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	opts := releaseVerifyOptions(t)
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	facts := manifest.RuntimeFacts{
		Platform:       "linux",
		Architecture:   "x86_64",
		AgentVersion:   "1.0.0",
		EnabledModules: []string{"remote-desktop"},
	}

	descriptor := buildDescriptor(
		manifestJSON,
		"1.0.0",
		hashHex,
		manifest.ManifestBriefing{DefaultMode: manifest.DeliveryAutomatic, AutoUpdate: true},
	)

	_, err = plugins.StageRemoteDesktopEngine(context.Background(), manager, server.Client(), server.URL, "agent-1", "", "stage-test", facts, descriptor)
	if err == nil {
		t.Fatal("expected staging to be blocked")
	}
	if !strings.Contains(err.Error(), "architecture") {
		t.Fatalf("expected architecture error, got %v", err)
	}
	if artifactRequested.Load() {
		t.Fatal("expected no artifact download attempts")
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected snapshot entry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallBlocked {
		t.Fatalf("expected blocked status, got %s", install.Status)
	}
	if !strings.Contains(install.Error, "architecture") {
		t.Fatalf("expected architecture message, got %q", install.Error)
	}
}

func TestStageRemoteDesktopEngineBlocksIncompatibleAgentVersion(t *testing.T) {
	t.Parallel()

	hashHex := strings.Repeat("ef", 32)
	signatureValue := releaseSignatureFor(t, hashHex)
	manifestJSON := fmt.Sprintf(`{
                "id": "remote-desktop-engine",
                "name": "Remote Desktop Engine",
                "version": "1.0.0",
                "entry": "remote-desktop-engine/remote-desktop-engine",
                "repositoryUrl": "https://github.com/rootbay/tenvy-client",
                "license": {"spdxId": "MIT"},
                "requirements": {"minAgentVersion": "5.0.0"},
                "distribution": {"defaultMode": "manual", "autoUpdate": false, "signature": "ed25519", "signatureHash": "%[1]s", "signatureSigner": "%[2]s", "signatureValue": "%[3]s", "signatureTimestamp": "%[4]s"},
                "package": {"artifact": "remote-desktop-engine.zip", "hash": "%[1]s"}
        }`, hashHex, releaseSigner, signatureValue, releaseSignedAtStamp)

	var artifactRequested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/artifact") {
			artifactRequested.Store(true)
			t.Fatalf("artifact download should not be attempted")
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, manifestJSON); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	opts := releaseVerifyOptions(t)
	manager, err := plugins.NewManager(root, log.New(io.Discard, "", 0), opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	facts := manifest.RuntimeFacts{
		Platform:       "linux",
		Architecture:   "x86_64",
		AgentVersion:   "1.0.0",
		EnabledModules: []string{"remote-desktop"},
	}

	descriptor := buildDescriptor(
		manifestJSON,
		"1.0.0",
		hashHex,
		manifest.ManifestBriefing{DefaultMode: manifest.DeliveryAutomatic, AutoUpdate: true},
	)

	_, err = plugins.StageRemoteDesktopEngine(context.Background(), manager, server.Client(), server.URL, "agent-1", "", "stage-test", facts, descriptor)
	if err == nil {
		t.Fatal("expected staging to be blocked")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected version error, got %v", err)
	}
	if artifactRequested.Load() {
		t.Fatal("expected no artifact download attempts")
	}

	snapshot := manager.Snapshot()
	if snapshot == nil || len(snapshot.Installations) != 1 {
		t.Fatalf("expected snapshot entry, got %#v", snapshot)
	}
	install := snapshot.Installations[0]
	if install.Status != manifest.InstallBlocked {
		t.Fatalf("expected blocked status, got %s", install.Status)
	}
	if !strings.Contains(install.Error, "version") {
		t.Fatalf("expected version message, got %q", install.Error)
	}
}

func buildRemoteDesktopArtifact(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, payload := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := entry.Write(payload); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func buildTarGzArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for name, payload := range files {
		header := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(payload)),
			Typeflag: tar.TypeReg,
		}
		if strings.HasSuffix(name, "/") {
			header.Typeflag = tar.TypeDir
			header.Size = 0
			header.Mode = 0o755
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if _, err := writer.Write(payload); err != nil {
			t.Fatalf("write tar payload: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

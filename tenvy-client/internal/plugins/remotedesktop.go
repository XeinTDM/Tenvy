package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

const RemoteDesktopEnginePluginID = "remote-desktop-engine"

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// RemoteDesktopStageResult describes the outcome of a staging attempt for the
// remote desktop engine.
type RemoteDesktopStageResult struct {
	EntryPath string
	Manifest  manifest.Manifest
	Updated   bool
}

// RemoteDesktopAutoSyncAllowed reports whether the manifest descriptor permits
// automatic synchronization and staging of the remote desktop engine. Manual
// delivery mode (or equivalent policy hints) disable automatic staging and
// require an explicit operator action.
func RemoteDesktopAutoSyncAllowed(descriptor manifest.ManifestDescriptor) bool {
	return autoSyncAllowed(descriptor)
}

// StageRemoteDesktopEngine ensures the remote desktop engine plugin is staged on
// disk, verifying signatures and hashes before unpacking the artifact into the
// plugin root. The returned result describes the installed manifest and the
// resolved entry path for launching the engine binary.
func StageRemoteDesktopEngine(
	ctx context.Context,
	manager *Manager,
	client HTTPDoer,
	baseURL, agentID, authKey, userAgent string,
	runtimeFacts manifest.RuntimeFacts,
	descriptor manifest.ManifestDescriptor,
) (RemoteDesktopStageResult, error) {
	var result RemoteDesktopStageResult

	if manager == nil {
		return result, errors.New("plugin manager not initialized")
	}
	if client == nil {
		return result, errors.New("http client not provided")
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return result, errors.New("controller base url not provided")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return result, errors.New("agent identifier not provided")
	}

	logger := manager.logger
	manager.stageMu.Lock()
	defer manager.stageMu.Unlock()

	if err := os.MkdirAll(manager.root, 0o755); err != nil {
		return result, fmt.Errorf("ensure plugin root: %w", err)
	}

	pluginID := strings.TrimSpace(descriptor.PluginID)
	if pluginID == "" {
		pluginID = RemoteDesktopEnginePluginID
	}
	if !strings.EqualFold(pluginID, RemoteDesktopEnginePluginID) {
		return result, fmt.Errorf("plugin %s is not supported for remote desktop staging", descriptor.PluginID)
	}

	pluginDir := filepath.Join(manager.root, RemoteDesktopEnginePluginID)
	manifestPath := filepath.Join(pluginDir, manifestFileName)

	if cached, entryPath, ok := reuseRemoteDesktopInstallation(manifestPath, pluginDir, descriptor); ok {
		result.Manifest = *cached
		result.EntryPath = entryPath
		result.Updated = false
		return result, nil
	}

	manualRequested := strings.TrimSpace(descriptor.ManualPushAt) != ""

	if !RemoteDesktopAutoSyncAllowed(descriptor) && !manualRequested {
		version := strings.TrimSpace(descriptor.Version)
		message := "remote desktop plugin automatic staging disabled by policy"
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, version, manifest.InstallDisabled, message)
		return result, errors.New(message)
	}

	manifestURL, artifactURL := pluginEndpoints(baseURL, agentID, pluginID)

	manifestData, mf, err := fetchPluginManifest(
		ctx,
		client,
		manifestURL,
		authKey,
		userAgent,
		descriptor.ManifestDigest,
	)
	if err != nil {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, "", manifest.InstallError, err.Error())
		return result, err
	}

	result.Manifest = mf

	if !strings.EqualFold(strings.TrimSpace(mf.ID), pluginID) {
		message := fmt.Sprintf("unexpected manifest id %s", mf.ID)
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, message)
		return result, errors.New(message)
	}

	verificationResult, verifyErr := manifest.VerifySignature(mf, manager.verificationOptions())
	if verifyErr != nil {
		message := fmt.Sprintf("signature verification failed: %s", signatureErrorMessage(verifyErr))
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallBlocked, message)
		return result, fmt.Errorf(message)
	}
	if verificationResult == nil || !verificationResult.Trusted {
		message := fmt.Sprintf("signature not trusted: %s", signatureUntrustedReason(mf, verificationResult))
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallBlocked, message)
		return result, errors.New(message)
	}

	if err := manifest.CheckRuntimeCompatibility(mf, runtimeFacts); err != nil {
		message := fmt.Sprintf("plugin requirements not satisfied: %s", err.Error())
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallBlocked, message)
		return result, errors.New(message)
	}

	artifactRef := strings.TrimSpace(mf.Package.Artifact)
	if artifactRef == "" || strings.ContainsAny(artifactRef, "/\\") {
		message := "manifest artifact path is invalid"
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, message)
		return result, errors.New(message)
	}

	artifactRel := filepath.Clean(artifactRef)
	if artifactRel == "" || artifactRel == "." || strings.HasPrefix(artifactRel, "..") {
		message := "manifest artifact path is invalid"
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, message)
		return result, errors.New(message)
	}
	entryRel := filepath.Clean(filepath.FromSlash(mf.Entry))
	if entryRel == "" || strings.HasPrefix(entryRel, "..") {
		message := "manifest entry path is invalid"
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, message)
		return result, errors.New(message)
	}

	artifactPath := filepath.Join(pluginDir, artifactRel)
	entryPath := filepath.Join(pluginDir, entryRel)

	if upToDate, err := installationUpToDate(manifestPath, artifactPath, entryPath, manifestData, mf); err == nil && upToDate {
		result.EntryPath = entryPath
		result.Updated = false
		return result, nil
	}

	stagingDir, err := os.MkdirTemp(manager.root, "remote-desktop-engine-")
	if err != nil {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, fmt.Sprintf("create staging directory: %v", err))
		return result, fmt.Errorf("create staging directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(stagingDir)
		}
	}()

	stagingManifest := filepath.Join(stagingDir, manifestFileName)
	if err := os.WriteFile(stagingManifest, manifestData, 0o644); err != nil {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, fmt.Sprintf("write manifest: %v", err))
		return result, fmt.Errorf("write manifest: %w", err)
	}

	stagingArtifact := filepath.Join(stagingDir, artifactRel)
	if err := os.MkdirAll(filepath.Dir(stagingArtifact), 0o755); err != nil {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, fmt.Sprintf("prepare artifact directory: %v", err))
		return result, fmt.Errorf("prepare artifact directory: %w", err)
	}

	if err := downloadPluginArtifact(ctx, client, artifactURL, authKey, userAgent, stagingArtifact); err != nil {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, err.Error())
		return result, err
	}

	if hash := strings.TrimSpace(mf.Package.Hash); hash != "" {
		sum, hashErr := fileHash(stagingArtifact)
		if hashErr != nil {
			manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, fmt.Sprintf("compute artifact hash: %v", hashErr))
			return result, fmt.Errorf("compute artifact hash: %w", hashErr)
		}
		if !strings.EqualFold(hash, sum) {
			message := "artifact hash mismatch"
			manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, message)
			return result, errors.New(message)
		}
	}

	loweredArtifact := strings.ToLower(artifactRel)
	var unpackErr error
	switch {
	case strings.EqualFold(filepath.Ext(artifactRel), ".zip"):
		unpackErr = unpackZipArchive(stagingArtifact, stagingDir, entryRel, manager.secret)
	case strings.HasSuffix(loweredArtifact, ".tar.gz"), strings.HasSuffix(loweredArtifact, ".tgz"):
		unpackErr = unpackTarGzArchive(stagingArtifact, stagingDir, entryRel, manager.secret)
	}
	if unpackErr != nil {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, unpackErr.Error())
		return result, unpackErr
	}

	stagedEntry := filepath.Join(stagingDir, entryRel)
	if info, err := os.Stat(stagedEntry); err != nil || info.IsDir() {
		message := "engine entry binary missing from artifact"
		if err != nil {
			message = fmt.Sprintf("engine entry verification failed: %v", err)
		}
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, message)
		return result, errors.New(message)
	}

	if err := os.RemoveAll(pluginDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, fmt.Sprintf("remove previous installation: %v", err))
		return result, fmt.Errorf("remove previous installation: %w", err)
	}

	if err := os.Rename(stagingDir, pluginDir); err != nil {
		manager.recordInstallStatusLocked(RemoteDesktopEnginePluginID, mf.Version, manifest.InstallError, fmt.Sprintf("activate staged plugin: %v", err))
		return result, fmt.Errorf("activate staged plugin: %w", err)
	}
	cleanup = false

	if err := manager.clearInstallStatusLocked(RemoteDesktopEnginePluginID); err != nil && logger != nil {
		logger.Printf("remote desktop: failed to clear plugin status: %v", err)
	}

	result.EntryPath = filepath.Join(pluginDir, entryRel)
	result.Updated = true
	return result, nil
}

func reuseRemoteDesktopInstallation(
	manifestPath, pluginDir string,
	descriptor manifest.ManifestDescriptor,
) (*manifest.Manifest, string, bool) {
	expected := strings.TrimSpace(descriptor.ManifestDigest)
	if expected == "" {
		return nil, "", false
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", false
	}
	sum := sha256.Sum256(data)
	digest := fmt.Sprintf("%x", sum[:])
	if !strings.EqualFold(digest, expected) {
		return nil, "", false
	}

	var mf manifest.Manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, "", false
	}

	artifactRel := filepath.Clean(filepath.FromSlash(mf.Package.Artifact))
	entryRel := filepath.Clean(filepath.FromSlash(mf.Entry))
	if artifactRel == "" || entryRel == "" {
		return nil, "", false
	}

	artifactPath := filepath.Join(pluginDir, artifactRel)
	entryPath := filepath.Join(pluginDir, entryRel)
	upToDate, err := installationUpToDate(manifestPath, artifactPath, entryPath, data, mf)
	if err != nil || !upToDate {
		return nil, "", false
	}

	return &mf, entryPath, true
}

func installationUpToDate(manifestPath, artifactPath, entryPath string, expectedManifest []byte, mf manifest.Manifest) (bool, error) {
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(manifestData, expectedManifest) {
		return false, nil
	}
	if _, err := os.Stat(entryPath); err != nil {
		return false, err
	}
	if strings.TrimSpace(mf.Package.Hash) == "" {
		if _, err := os.Stat(artifactPath); err != nil {
			return false, err
		}
		return true, nil
	}
	currentHash, err := fileHash(artifactPath)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(currentHash, mf.Package.Hash), nil
}

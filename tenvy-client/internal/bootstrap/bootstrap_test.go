package bootstrap

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandInstallsLoaderWhenMissing(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}

	payload := []byte("fresh-loader")
	hash := sha256.Sum256(payload)
	metadata := &LoaderMetadata{
		Version:    "1.2.3",
		Checksum:   fmt.Sprintf("%x", hash[:]),
		Signature:  "signed",
		Executable: "tenvy-client-loader",
	}

	downloads := 0
	downloader := LoaderDownloaderFunc(func(ctx context.Context, meta LoaderMetadata) (LoaderPackage, error) {
		downloads++
		if meta.Version != metadata.Version {
			t.Fatalf("unexpected version requested: %q", meta.Version)
		}
		archive := buildLoaderArchive(t, meta.Executable, payload)
		return LoaderPackage{Archive: archive}, nil
	})

	var verified bool
	verifier := LoaderSignatureVerifierFunc(func(ctx context.Context, path string, meta LoaderMetadata) error {
		if meta.Signature != metadata.Signature {
			return fmt.Errorf("unexpected signature %q", meta.Signature)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, payload) {
			return fmt.Errorf("unexpected loader payload: %q", data)
		}
		verified = true
		return nil
	})

	cmd, err := Command(context.Background(), Options{
		ExecutablePath:          stubPath,
		DesiredLoader:           metadata,
		LoaderDownloader:        downloader,
		LoaderSignatureVerifier: verifier,
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}

	expectedLoaderPath := filepath.Join(tempDir, defaultLoaderDirectory, metadata.Executable)
	if cmd.Path != expectedLoaderPath {
		t.Fatalf("expected loader path %q, got %q", expectedLoaderPath, cmd.Path)
	}

	record, err := readStoredLoaderMetadata(filepath.Join(tempDir, defaultLoaderDirectory, loaderMetadataFileName))
	if err != nil {
		t.Fatalf("read loader metadata: %v", err)
	}
	if record.Version != metadata.Version {
		t.Fatalf("expected stored version %q, got %q", metadata.Version, record.Version)
	}
	if !strings.EqualFold(record.Checksum, metadata.Checksum) {
		t.Fatalf("expected stored checksum %q, got %q", metadata.Checksum, record.Checksum)
	}
	if record.Signature != metadata.Signature {
		t.Fatalf("expected stored signature %q, got %q", metadata.Signature, record.Signature)
	}
	if record.Executable != metadata.Executable {
		t.Fatalf("expected stored executable %q, got %q", metadata.Executable, record.Executable)
	}
	if record.InstalledAt.IsZero() {
		t.Fatalf("expected installation timestamp recorded")
	}
	if downloads != 1 {
		t.Fatalf("expected exactly one download, got %d", downloads)
	}
	if !verified {
		t.Fatalf("expected signature verification to run")
	}
}

func TestCommandUpdatesStaleLoader(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}

	loaderDir := filepath.Join(tempDir, defaultLoaderDirectory)
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		t.Fatalf("failed to create loader dir: %v", err)
	}

	oldPayload := []byte("old-loader")
	oldHash := sha256.Sum256(oldPayload)
	loaderPath := filepath.Join(loaderDir, "tenvy-client-loader")
	if err := os.WriteFile(loaderPath, oldPayload, 0o755); err != nil {
		t.Fatalf("failed to seed loader file: %v", err)
	}
	originalRecord := storedLoaderMetadata{
		LoaderMetadata: LoaderMetadata{
			Version:    "0.1.0",
			Checksum:   fmt.Sprintf("%x", oldHash[:]),
			Executable: "tenvy-client-loader",
		},
		InstalledAt: time.Now().Add(-time.Hour),
	}
	if err := writeStoredLoaderMetadata(filepath.Join(loaderDir, loaderMetadataFileName), originalRecord); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}

	newPayload := []byte("new-loader")
	newHash := sha256.Sum256(newPayload)
	target := &LoaderMetadata{
		Version:    "2.0.0",
		Checksum:   fmt.Sprintf("%x", newHash[:]),
		Executable: "tenvy-client-loader",
	}

	downloads := 0
	downloader := LoaderDownloaderFunc(func(ctx context.Context, meta LoaderMetadata) (LoaderPackage, error) {
		downloads++
		if meta.Version != target.Version {
			t.Fatalf("unexpected version requested: %q", meta.Version)
		}
		return LoaderPackage{Binary: append([]byte(nil), newPayload...), Mode: 0o755}, nil
	})

	cmd, err := Command(context.Background(), Options{
		ExecutablePath:   stubPath,
		DesiredLoader:    target,
		LoaderDownloader: downloader,
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}
	if downloads != 1 {
		t.Fatalf("expected exactly one download, got %d", downloads)
	}

	expectedPath := filepath.Join(loaderDir, target.Executable)
	if cmd.Path != expectedPath {
		t.Fatalf("expected loader path %q, got %q", expectedPath, cmd.Path)
	}
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read loader: %v", err)
	}
	if !bytes.Equal(data, newPayload) {
		t.Fatalf("unexpected loader contents: %q", data)
	}

	record, err := readStoredLoaderMetadata(filepath.Join(loaderDir, loaderMetadataFileName))
	if err != nil {
		t.Fatalf("read loader metadata: %v", err)
	}
	if record.Version != target.Version {
		t.Fatalf("expected version %q, got %q", target.Version, record.Version)
	}
	if !strings.EqualFold(record.Checksum, target.Checksum) {
		t.Fatalf("expected checksum %q, got %q", target.Checksum, record.Checksum)
	}
	if record.Signature != "" {
		t.Fatalf("expected no signature, got %q", record.Signature)
	}
	if record.InstalledAt.Before(originalRecord.InstalledAt) {
		t.Fatalf("expected installation timestamp to be updated")
	}
}

func TestCommandRepairsCorruptedLoader(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}

	loaderDir := filepath.Join(tempDir, defaultLoaderDirectory)
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		t.Fatalf("failed to create loader dir: %v", err)
	}

	expectedPayload := []byte("clean-loader")
	hash := sha256.Sum256(expectedPayload)
	metadata := &LoaderMetadata{
		Version:    "3.1.4",
		Checksum:   fmt.Sprintf("%x", hash[:]),
		Executable: "tenvy-client-loader",
	}

	record := storedLoaderMetadata{
		LoaderMetadata: *metadata,
		InstalledAt:    time.Now().Add(-2 * time.Hour),
	}
	if err := writeStoredLoaderMetadata(filepath.Join(loaderDir, loaderMetadataFileName), record); err != nil {
		t.Fatalf("failed to seed metadata: %v", err)
	}

	corruptedPath := filepath.Join(loaderDir, metadata.Executable)
	if err := os.WriteFile(corruptedPath, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("failed to seed corrupted loader: %v", err)
	}

	downloads := 0
	downloader := LoaderDownloaderFunc(func(ctx context.Context, meta LoaderMetadata) (LoaderPackage, error) {
		downloads++
		if meta.Version != metadata.Version {
			t.Fatalf("unexpected version requested: %q", meta.Version)
		}
		return LoaderPackage{Binary: append([]byte(nil), expectedPayload...), Mode: 0o755}, nil
	})

	cmd, err := Command(context.Background(), Options{
		ExecutablePath:   stubPath,
		DesiredLoader:    metadata,
		LoaderDownloader: downloader,
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}
	if downloads != 1 {
		t.Fatalf("expected repair download, got %d", downloads)
	}

	expectedPath := filepath.Join(loaderDir, metadata.Executable)
	if cmd.Path != expectedPath {
		t.Fatalf("expected loader path %q, got %q", expectedPath, cmd.Path)
	}
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read loader: %v", err)
	}
	if !bytes.Equal(data, expectedPayload) {
		t.Fatalf("unexpected loader contents after repair: %q", data)
	}

	updated, err := readStoredLoaderMetadata(filepath.Join(loaderDir, loaderMetadataFileName))
	if err != nil {
		t.Fatalf("read loader metadata: %v", err)
	}
	if updated.Version != metadata.Version {
		t.Fatalf("expected version %q, got %q", metadata.Version, updated.Version)
	}
	if !strings.EqualFold(updated.Checksum, metadata.Checksum) {
		t.Fatalf("expected checksum %q, got %q", metadata.Checksum, updated.Checksum)
	}
	if updated.InstalledAt.Before(record.InstalledAt) {
		t.Fatalf("expected refreshed installation timestamp")
	}
}

func TestCommandFailsWhenChecksumMismatch(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}

	expected := []byte("loader")
	checksum := sha256.Sum256(expected)
	metadata := &LoaderMetadata{
		Version:    "9.9.9",
		Checksum:   fmt.Sprintf("%x", checksum[:]),
		Executable: "tenvy-client-loader",
	}

	downloader := LoaderDownloaderFunc(func(ctx context.Context, meta LoaderMetadata) (LoaderPackage, error) {
		return LoaderPackage{Binary: []byte("corrupt")}, nil
	})

	_, err := Command(context.Background(), Options{
		ExecutablePath:   stubPath,
		DesiredLoader:    metadata,
		LoaderDownloader: downloader,
	})
	if err == nil {
		t.Fatalf("expected checksum error")
	}
	if !errors.Is(err, errChecksumMismatch) {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}

	metadataPath := filepath.Join(tempDir, defaultLoaderDirectory, loaderMetadataFileName)
	if _, statErr := os.Stat(metadataPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected metadata file to be absent, got %v", statErr)
	}
}

func TestCommandUsesOverride(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	loaderPath := filepath.Join(tempDir, "loader.exe")

	if err := os.WriteFile(stubPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}
	if err := os.WriteFile(loaderPath, []byte("loader"), 0o644); err != nil {
		t.Fatalf("failed to write loader file: %v", err)
	}

	ctx := context.Background()
	cmd, err := Command(ctx, Options{
		ExecutablePath: stubPath,
		OverridePath:   loaderPath,
		LoaderArgs:     []string{"--hello", "world"},
		BaseEnv:        []string{"FOO=BAR"},
		AdditionalEnv:  map[string]string{"EXTRA": "1"},
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}

	if cmd.Path != loaderPath {
		t.Fatalf("expected loader path %q, got %q", loaderPath, cmd.Path)
	}

	expectedArgs := append([]string{loaderPath}, "--hello", "world")
	if !slicesEqual(cmd.Args, expectedArgs) {
		t.Fatalf("unexpected args: got %v, want %v", cmd.Args, expectedArgs)
	}

	env := envMap(cmd.Env)
	if env["FOO"] != "BAR" {
		t.Fatalf("expected FOO=BAR in environment, got %q", env["FOO"])
	}
	if env["EXTRA"] != "1" {
		t.Fatalf("expected EXTRA=1 in environment, got %q", env["EXTRA"])
	}
	if env["TENVY_LOADER_EXECUTABLE"] != loaderPath {
		t.Fatalf("expected TENVY_LOADER_EXECUTABLE=%q, got %q", loaderPath, env["TENVY_LOADER_EXECUTABLE"])
	}
}

func TestCommandDiscoversLoaderRelativeToStub(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	loaderDir := filepath.Join(tempDir, "loader")
	loaderPath := filepath.Join(loaderDir, "tenvy-client-loader")

	if err := os.WriteFile(stubPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		t.Fatalf("failed to create loader dir: %v", err)
	}
	if err := os.WriteFile(loaderPath, []byte("loader"), 0o644); err != nil {
		t.Fatalf("failed to write loader file: %v", err)
	}

	cmd, err := Command(context.Background(), Options{
		ExecutablePath: stubPath,
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}

	if cmd.Path != loaderPath {
		t.Fatalf("expected loader path %q, got %q", loaderPath, cmd.Path)
	}

	if cmd.Dir != loaderDir {
		t.Fatalf("expected command dir %q, got %q", loaderDir, cmd.Dir)
	}
}

func TestCommandAcceptsRelativeOverride(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	loaderPath := filepath.Join(tempDir, "bin", "loader.exe")

	if err := os.WriteFile(stubPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(loaderPath), 0o755); err != nil {
		t.Fatalf("failed to create loader dir: %v", err)
	}
	if err := os.WriteFile(loaderPath, []byte("loader"), 0o644); err != nil {
		t.Fatalf("failed to write loader file: %v", err)
	}

	cmd, err := Command(context.Background(), Options{
		ExecutablePath: stubPath,
		OverridePath:   filepath.Join("bin", "loader.exe"),
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}

	if cmd.Path != loaderPath {
		t.Fatalf("expected loader path %q, got %q", loaderPath, cmd.Path)
	}
}

func TestCommandMissingLoader(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("failed to write stub file: %v", err)
	}

	if _, err := Command(context.Background(), Options{ExecutablePath: stubPath}); err == nil {
		t.Fatalf("expected error when loader missing")
	}
}

func TestBuildSearchDirsIncludesAdditionalPaths(t *testing.T) {
	t.Parallel()

	stubDir := filepath.Join("/tmp", "stub")
	dirs := buildSearchDirs(stubDir, []string{"bin", "/opt/tenvy"})

	expected := []string{
		filepath.Clean(stubDir),
		filepath.Join(filepath.Clean(stubDir), "loader"),
		filepath.Join(filepath.Clean(stubDir), "bin"),
		filepath.Clean("/opt/tenvy"),
	}
	if !slicesEqual(dirs, expected) {
		t.Fatalf("unexpected dirs: got %v, want %v", dirs, expected)
	}
}

func TestEnvironmentDefaultBase(t *testing.T) {
	stubPath := mustWriteTempFile(t)
	loaderPath := mustWriteTempFile(t)

	t.Setenv("TEST_ENV_KEY", "VALUE")
	cmd, err := Command(context.Background(), Options{
		ExecutablePath: stubPath,
		OverridePath:   loaderPath,
		AdditionalEnv:  map[string]string{"CUSTOM": "yes"},
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}

	env := envMap(cmd.Env)
	if env["CUSTOM"] != "yes" {
		t.Fatalf("expected CUSTOM env override, got %q", env["CUSTOM"])
	}
	if env["TEST_ENV_KEY"] != "VALUE" {
		t.Fatalf("expected TEST_ENV_KEY inherited, got %q", env["TEST_ENV_KEY"])
	}
}

func TestCommandInstallsLoaderViaHTTPDownloader(t *testing.T) {
	t.Parallel()

	payload := []byte("http-loader")
	hash := sha256.Sum256(payload)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signature := ed25519.Sign(priv, []byte(hex.EncodeToString(hash[:])))
	signatureString := fmt.Sprintf("ed25519:%s:%s", hex.EncodeToString(pub), hex.EncodeToString(signature))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("write payload: %v", err)
		}
	}))
	defer server.Close()

	downloader, err := NewHTTPDownloader(HTTPDownloaderConfig{
		Client:       server.Client(),
		URL:          server.URL,
		ArtifactType: LoaderArtifactTypeBinary,
	})
	if err != nil {
		t.Fatalf("NewHTTPDownloader error: %v", err)
	}

	verifier := NewLoaderSignatureVerifier()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	metadata := &LoaderMetadata{
		Version:    "1.0.0",
		Checksum:   fmt.Sprintf("%x", hash[:]),
		Signature:  signatureString,
		Executable: "tenvy-client-loader",
	}

	cmd, err := Command(context.Background(), Options{
		ExecutablePath:          stubPath,
		DesiredLoader:           metadata,
		LoaderDownloader:        downloader,
		LoaderSignatureVerifier: verifier,
	})
	if err != nil {
		t.Fatalf("Command returned error: %v", err)
	}

	installedPath := filepath.Join(tempDir, defaultLoaderDirectory, metadata.Executable)
	if cmd.Path != installedPath {
		t.Fatalf("expected loader path %q, got %q", installedPath, cmd.Path)
	}

	installed, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("read installed loader: %v", err)
	}
	if !bytes.Equal(installed, payload) {
		t.Fatalf("unexpected loader contents: %q", installed)
	}

	record, err := readStoredLoaderMetadata(filepath.Join(tempDir, defaultLoaderDirectory, loaderMetadataFileName))
	if err != nil {
		t.Fatalf("read stored metadata: %v", err)
	}
	if record.Version != metadata.Version {
		t.Fatalf("expected version %q, got %q", metadata.Version, record.Version)
	}
	if !strings.EqualFold(record.Checksum, metadata.Checksum) {
		t.Fatalf("expected checksum %q, got %q", metadata.Checksum, record.Checksum)
	}
	if record.Signature != metadata.Signature {
		t.Fatalf("expected signature %q, got %q", metadata.Signature, record.Signature)
	}
	if record.InstalledAt.IsZero() {
		t.Fatalf("expected installation timestamp to be recorded")
	}
}

func TestNewHTTPDownloaderValidatesInput(t *testing.T) {
	t.Parallel()

	if _, err := NewHTTPDownloader(HTTPDownloaderConfig{}); err == nil {
		t.Fatalf("expected url validation error")
	}

	if _, err := NewHTTPDownloader(HTTPDownloaderConfig{URL: "http://example", ArtifactType: "invalid"}); err == nil {
		t.Fatalf("expected artifact type error")
	}
}

func TestNewLoaderSignatureVerifierSHA256(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	loaderPath := filepath.Join(tempDir, "loader")
	payload := []byte("hello")
	if err := os.WriteFile(loaderPath, payload, 0o600); err != nil {
		t.Fatalf("write loader: %v", err)
	}
	hash := sha256.Sum256(payload)
	verifier := NewLoaderSignatureVerifier()
	metadata := LoaderMetadata{Checksum: hex.EncodeToString(hash[:]), Signature: hex.EncodeToString(hash[:])}
	if err := verifier.Verify(context.Background(), loaderPath, metadata); err != nil {
		t.Fatalf("verify returned error: %v", err)
	}
}

func TestNewLoaderSignatureVerifierEd25519(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	loaderPath := filepath.Join(tempDir, "loader")
	payload := []byte("signed-loader")
	if err := os.WriteFile(loaderPath, payload, 0o600); err != nil {
		t.Fatalf("write loader: %v", err)
	}

	hash := sha256.Sum256(payload)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signature := ed25519.Sign(priv, []byte(hex.EncodeToString(hash[:])))
	metadata := LoaderMetadata{
		Checksum:  hex.EncodeToString(hash[:]),
		Signature: fmt.Sprintf("ed25519:%s:%s", hex.EncodeToString(pub), hex.EncodeToString(signature)),
	}

	verifier := NewLoaderSignatureVerifier()
	if err := verifier.Verify(context.Background(), loaderPath, metadata); err != nil {
		t.Fatalf("verify returned error: %v", err)
	}
}

func TestNewLoaderSignatureVerifierRejectsInvalid(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	loaderPath := filepath.Join(tempDir, "loader")
	if err := os.WriteFile(loaderPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write loader: %v", err)
	}
	verifier := NewLoaderSignatureVerifier()
	metadata := LoaderMetadata{Signature: "ed25519::"}
	if err := verifier.Verify(context.Background(), loaderPath, metadata); err == nil {
		t.Fatalf("expected error for invalid signature")
	}
}

func buildLoaderArchive(t *testing.T, name string, payload []byte) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: filepath.ToSlash(name)}
	header.Method = zip.Deflate
	header.SetMode(0o755)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatalf("create archive entry: %v", err)
	}
	if _, err := entry.Write(payload); err != nil {
		t.Fatalf("write archive entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive writer: %v", err)
	}
	return buffer.Bytes()
}

func mustWriteTempFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func envMap(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, entry := range env {
		if entry == "" {
			continue
		}
		key, value, found := strings.Cut(entry, "=")
		if !found {
			result[entry] = ""
			continue
		}
		result[key] = value
	}
	return result
}

func TestEnsureFileRejectsDirectories(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	if err := ensureFile(tempDir, realFileSystem{}); err == nil {
		t.Fatalf("expected directory to be rejected")
	}
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	stubPath := filepath.Join("/opt", "tenvy", "stub")
	rel := filepath.Join("..", "bin", "loader")
	normalized := normalizePath(stubPath, rel)
	expected := filepath.Join("/opt", "tenvy", "..", "bin", "loader")
	if normalized != filepath.Clean(expected) {
		t.Fatalf("unexpected normalized path: got %q, want %q", normalized, filepath.Clean(expected))
	}
}

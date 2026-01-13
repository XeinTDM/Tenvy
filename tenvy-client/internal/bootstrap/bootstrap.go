package bootstrap

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileSystem interface {
	Stat(name string) (fs.FileInfo, error)
}

type realFileSystem struct{}

func (realFileSystem) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

type LoaderMetadata struct {
	Version    string `json:"version"`
	Checksum   string `json:"checksum"`
	Signature  string `json:"signature,omitempty"`
	Executable string `json:"executable"`
}

type LoaderPackage struct {
	Archive []byte
	Binary  []byte
	Mode    fs.FileMode
}

type LoaderDownloader interface {
	Download(ctx context.Context, metadata LoaderMetadata) (LoaderPackage, error)
}

type LoaderDownloaderFunc func(ctx context.Context, metadata LoaderMetadata) (LoaderPackage, error)

func (f LoaderDownloaderFunc) Download(ctx context.Context, metadata LoaderMetadata) (LoaderPackage, error) {
	return f(ctx, metadata)
}

type LoaderSignatureVerifier interface {
	Verify(ctx context.Context, loaderPath string, metadata LoaderMetadata) error
}

type LoaderSignatureVerifierFunc func(ctx context.Context, loaderPath string, metadata LoaderMetadata) error

func (f LoaderSignatureVerifierFunc) Verify(ctx context.Context, loaderPath string, metadata LoaderMetadata) error {
	return f(ctx, loaderPath, metadata)
}

type Options struct {
	// ExecutablePath is the absolute path to the stub executable. Required.
	ExecutablePath string
	// OverridePath points to the loader executable, bypassing discovery.
	OverridePath string
	LoaderArgs   []string
	// BaseEnv is the environment inherited by the loader. Defaults to os.Environ.
	BaseEnv []string
	// AdditionalEnv defines extra environment variables to inject.
	AdditionalEnv map[string]string
	// SearchDirs are extra directories inspected for the loader.
	SearchDirs     []string
	CandidateNames []string
	// FileSystem powers file discovery. Defaults to the real OS filesystem.
	FileSystem    FileSystem
	DesiredLoader *LoaderMetadata
	// LoaderDownloader fetches loader updates when missing or outdated.
	LoaderDownloader        LoaderDownloader
	LoaderSignatureVerifier LoaderSignatureVerifier
	// LoaderInstallDir overrides where loader artifacts are stored.
	LoaderInstallDir string
}

func Command(ctx context.Context, opts Options) (*exec.Cmd, error) {
	if strings.TrimSpace(opts.ExecutablePath) == "" {
		return nil, errors.New("executable path is required")
	}

	fsys := opts.FileSystem
	if fsys == nil {
		fsys = realFileSystem{}
	}

	if err := ensureLoaderReady(ctx, opts); err != nil {
		return nil, err
	}

	loaderPath, err := discoverLoader(opts, fsys)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, loaderPath, opts.LoaderArgs...)
	cmd.Env = buildEnvironment(opts, loaderPath)
	cmd.Dir = filepath.Dir(loaderPath)
	cmd.Stdin = os.Stdin
	return cmd, nil
}

func discoverLoader(opts Options, fsys FileSystem) (string, error) {
	override := strings.TrimSpace(opts.OverridePath)
	if override != "" {
		path := normalizePath(opts.ExecutablePath, override)
		if err := ensureFile(path, fsys); err != nil {
			return "", fmt.Errorf("loader override %q invalid: %w", override, err)
		}
		return path, nil
	}

	stubDir := filepath.Dir(opts.ExecutablePath)
	searchDirs := buildSearchDirs(stubDir, opts.SearchDirs)
	candidateNames := opts.CandidateNames
	if len(candidateNames) == 0 {
		candidateNames = defaultCandidateNames()
	}

	visited := make(map[string]struct{})
	for _, dir := range searchDirs {
		for _, name := range candidateNames {
			candidate := name
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(dir, candidate)
			}
			cleaned := filepath.Clean(candidate)
			if _, seen := visited[cleaned]; seen {
				continue
			}
			visited[cleaned] = struct{}{}

			if err := ensureFile(cleaned, fsys); err == nil {
				return cleaned, nil
			}
		}
	}

	return "", errors.New("loader executable not found")
}

func buildEnvironment(opts Options, loaderPath string) []string {
	base := opts.BaseEnv
	if base == nil {
		base = os.Environ()
	}

	merged := make(map[string]string, len(base)+len(opts.AdditionalEnv)+1)
	for _, entry := range base {
		if entry == "" {
			continue
		}
		key, value, found := strings.Cut(entry, "=")
		if !found {
			merged[key] = ""
			continue
		}
		merged[key] = value
	}

	for key, value := range opts.AdditionalEnv {
		merged[key] = value
	}

	merged["TENVY_LOADER_EXECUTABLE"] = loaderPath

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+merged[key])
	}
	return env
}

func ensureFile(path string, fsys FileSystem) error {
	if path == "" {
		return errors.New("empty path")
	}
	info, err := fsys.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

func buildSearchDirs(stubDir string, extra []string) []string {
	dirs := make([]string, 0, len(extra)+3)
	seen := make(map[string]struct{})
	add := func(path string) {
		cleaned := filepath.Clean(path)
		if _, exists := seen[cleaned]; exists {
			return
		}
		seen[cleaned] = struct{}{}
		dirs = append(dirs, cleaned)
	}

	if stubDir != "" {
		add(stubDir)
		add(filepath.Join(stubDir, "loader"))
		add(filepath.Join(stubDir, "bin"))
	}

	for _, dir := range extra {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := dir
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(stubDir, candidate)
		}
		add(candidate)
	}

	return dirs
}

func defaultCandidateNames() []string {
	return []string{
		"tenvy-client-loader",
		"tenvy-client-loader.exe",
		"loader",
		"loader.exe",
	}
}

func normalizePath(stubExecutable, path string) string {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned
	}
	return filepath.Join(filepath.Dir(stubExecutable), cleaned)
}

const (
	defaultLoaderDirectory  = "loader"
	loaderMetadataFileName  = "loader-metadata.json"
	metadataTempFilePattern = "loader-metadata-*.tmp"
)

var (
	errChecksumMismatch = errors.New("loader checksum mismatch")
	errInvalidMetadata  = errors.New("invalid loader metadata")
)

func ensureLoaderReady(ctx context.Context, opts Options) error {
	if strings.TrimSpace(opts.OverridePath) != "" {
		// External overrides bypass loader management.
		return nil
	}

	if opts.DesiredLoader == nil {
		return nil
	}

	target, err := normalizeLoaderMetadata(*opts.DesiredLoader)
	if err != nil {
		return err
	}

	installDir := resolveInstallDir(opts.ExecutablePath, strings.TrimSpace(opts.LoaderInstallDir))
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("prepare loader directory: %w", err)
	}

	metadataPath := filepath.Join(installDir, loaderMetadataFileName)
	loaderPath := filepath.Join(installDir, target.Executable)

	stored, err := readStoredLoaderMetadata(metadataPath)
	if err == nil {
		if loaderMetadataMatches(stored.LoaderMetadata, target) {
			if err := verifyLoaderChecksum(loaderPath, target.Checksum); err == nil {
				if err := verifyLoaderSignature(ctx, opts.LoaderSignatureVerifier, loaderPath, stored.LoaderMetadata); err != nil {
					return err
				}
				return nil
			} else if err != nil {
				if errors.Is(err, errChecksumMismatch) || errors.Is(err, os.ErrNotExist) {
					// Reinstall below.
				} else {
					return fmt.Errorf("verify loader checksum: %w", err)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInvalidMetadata) {
		return err
	}

	if opts.LoaderDownloader == nil {
		return errors.New("loader unavailable and no downloader configured")
	}

	pkg, err := opts.LoaderDownloader.Download(ctx, target)
	if err != nil {
		return fmt.Errorf("download loader: %w", err)
	}
	if err := installLoaderPackage(pkg, installDir, target.Executable); err != nil {
		return err
	}
	if err := verifyLoaderChecksum(loaderPath, target.Checksum); err != nil {
		return fmt.Errorf("validate loader checksum: %w", err)
	}
	if err := verifyLoaderSignature(ctx, opts.LoaderSignatureVerifier, loaderPath, target); err != nil {
		return err
	}

	record := storedLoaderMetadata{
		LoaderMetadata: target,
		InstalledAt:    time.Now().UTC(),
	}
	if err := writeStoredLoaderMetadata(metadataPath, record); err != nil {
		return err
	}
	return nil
}

func resolveInstallDir(stubPath, override string) string {
	stubDir := filepath.Dir(stubPath)
	if strings.TrimSpace(override) == "" {
		return filepath.Join(stubDir, defaultLoaderDirectory)
	}
	cleaned := filepath.Clean(override)
	if filepath.IsAbs(cleaned) {
		return cleaned
	}
	return filepath.Join(stubDir, cleaned)
}

func normalizeLoaderMetadata(meta LoaderMetadata) (LoaderMetadata, error) {
	trimmedVersion := strings.TrimSpace(meta.Version)
	if trimmedVersion == "" {
		return LoaderMetadata{}, errors.New("loader version is required")
	}
	cleanedExec, err := cleanRelativePath(meta.Executable)
	if err != nil {
		return LoaderMetadata{}, err
	}
	checksum := strings.ToLower(strings.TrimSpace(meta.Checksum))
	if checksum == "" {
		return LoaderMetadata{}, errors.New("loader checksum is required")
	}
	normalized := LoaderMetadata{
		Version:    trimmedVersion,
		Checksum:   checksum,
		Signature:  strings.TrimSpace(meta.Signature),
		Executable: cleanedExec,
	}
	return normalized, nil
}

func cleanRelativePath(path string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return "", errors.New("loader executable is required")
	}
	if filepath.IsAbs(cleaned) {
		return "", errors.New("loader executable must be relative")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", errors.New("loader executable escapes install directory")
	}
	return cleaned, nil
}

type storedLoaderMetadata struct {
	LoaderMetadata
	InstalledAt time.Time `json:"installedAt"`
}

func readStoredLoaderMetadata(path string) (storedLoaderMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return storedLoaderMetadata{}, err
	}
	var record storedLoaderMetadata
	if err := json.Unmarshal(data, &record); err != nil {
		return storedLoaderMetadata{}, fmt.Errorf("%w: decode loader metadata: %v", errInvalidMetadata, err)
	}
	normalized, err := normalizeLoaderMetadata(record.LoaderMetadata)
	if err != nil {
		return storedLoaderMetadata{}, fmt.Errorf("%w: %v", errInvalidMetadata, err)
	}
	record.LoaderMetadata = normalized
	return record, nil
}

func writeStoredLoaderMetadata(path string, metadata storedLoaderMetadata) error {
	payload := struct {
		LoaderMetadata
		InstalledAt time.Time `json:"installedAt"`
	}{LoaderMetadata: metadata.LoaderMetadata, InstalledAt: metadata.InstalledAt}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode loader metadata: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("prepare metadata directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), metadataTempFilePattern)
	if err != nil {
		return fmt.Errorf("create metadata temp file: %w", err)
	}
	tempPath := temp.Name()
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return fmt.Errorf("write metadata temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("finalize metadata temp file: %w", err)
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("set metadata permissions: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("persist metadata file: %w", err)
	}
	return nil
}

func loaderMetadataMatches(current, target LoaderMetadata) bool {
	return strings.EqualFold(current.Checksum, target.Checksum) &&
		current.Version == target.Version &&
		current.Signature == target.Signature &&
		current.Executable == target.Executable
}

func verifyLoaderChecksum(path, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return errors.New("expected loader checksum missing")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open loader: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("compute loader checksum: %w", err)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("%w: expected %s, got %s", errChecksumMismatch, expected, actual)
	}
	return nil
}

func verifyLoaderSignature(ctx context.Context, verifier LoaderSignatureVerifier, loaderPath string, metadata LoaderMetadata) error {
	if strings.TrimSpace(metadata.Signature) == "" {
		if verifier == nil {
			return nil
		}
		return verifier.Verify(ctx, loaderPath, metadata)
	}
	if verifier == nil {
		return errors.New("loader signature provided but verifier unavailable")
	}
	if err := verifier.Verify(ctx, loaderPath, metadata); err != nil {
		return fmt.Errorf("verify loader signature: %w", err)
	}
	return nil
}

func installLoaderPackage(pkg LoaderPackage, installDir, execRel string) error {
	if len(pkg.Archive) > 0 && len(pkg.Binary) > 0 {
		return errors.New("loader package ambiguous")
	}
	if len(pkg.Archive) == 0 && len(pkg.Binary) == 0 {
		return errors.New("loader package missing payload")
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("prepare loader directory: %w", err)
	}
	if len(pkg.Archive) > 0 {
		if err := installLoaderFromArchive(pkg.Archive, installDir); err != nil {
			return err
		}
	} else {
		if err := installLoaderBinary(pkg.Binary, installDir, execRel, pkg.Mode); err != nil {
			return err
		}
	}

	loaderPath := filepath.Join(installDir, execRel)
	if err := ensureLoaderExecutable(loaderPath); err != nil {
		return err
	}
	if _, err := os.Stat(loaderPath); err != nil {
		return fmt.Errorf("loader executable missing after install: %w", err)
	}
	return nil
}

func installLoaderFromArchive(payload []byte, dest string) error {
	reader := bytes.NewReader(payload)
	archive, err := zip.NewReader(reader, int64(len(payload)))
	if err != nil {
		return fmt.Errorf("open loader archive: %w", err)
	}
	for _, entry := range archive.File {
		if err := extractLoaderArchiveEntry(entry, dest); err != nil {
			return err
		}
	}
	return nil
}

func extractLoaderArchiveEntry(entry *zip.File, dest string) error {
	cleaned := filepath.Clean(entry.Name)
	if cleaned == "" || cleaned == "." {
		return nil
	}
	target := filepath.Join(dest, cleaned)
	if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
		return fmt.Errorf("loader archive entry escapes destination: %s", entry.Name)
	}
	if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("loader archive entry is a symlink: %s", entry.Name)
	}

	if entry.FileInfo().IsDir() {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("create loader directory: %w", err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("prepare loader path: %w", err)
	}

	reader, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open loader archive entry: %w", err)
	}
	defer reader.Close()

	temp, err := os.CreateTemp(filepath.Dir(target), "loader-entry-*.tmp")
	if err != nil {
		return fmt.Errorf("create loader temp file: %w", err)
	}
	tempPath := temp.Name()
	if _, err := io.Copy(temp, reader); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return fmt.Errorf("write loader archive entry: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("finalize loader archive entry: %w", err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("persist loader archive entry: %w", err)
	}
	if mode := entry.Mode(); mode != 0 {
		if err := os.Chmod(target, mode); err != nil {
			return fmt.Errorf("set loader entry permissions: %w", err)
		}
	}
	return nil
}

func installLoaderBinary(payload []byte, installDir, execRel string, mode fs.FileMode) error {
	if len(payload) == 0 {
		return errors.New("loader binary payload empty")
	}
	dest := filepath.Join(installDir, execRel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("prepare loader path: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(dest), "loader-*.tmp")
	if err != nil {
		return fmt.Errorf("create loader temp file: %w", err)
	}
	tempPath := temp.Name()
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return fmt.Errorf("write loader binary: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("finalize loader binary: %w", err)
	}
	if err := os.Rename(tempPath, dest); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("persist loader binary: %w", err)
	}
	finalMode := mode
	if finalMode == 0 {
		finalMode = 0o755
	}
	if err := os.Chmod(dest, finalMode); err != nil {
		return fmt.Errorf("set loader permissions: %w", err)
	}
	return nil
}

func ensureLoaderExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat loader: %w", err)
	}
	mode := info.Mode()
	if mode.IsDir() {
		return fmt.Errorf("loader path is a directory: %s", path)
	}
	if mode&0o111 != 0 {
		return nil
	}
	if err := os.Chmod(path, mode|0o111); err != nil {
		return fmt.Errorf("mark loader executable: %w", err)
	}
	return nil
}

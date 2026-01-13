package recovery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command                 = protocol.Command
	CommandResult           = protocol.CommandResult
	RecoveryCommandPayload  = protocol.RecoveryCommandPayload
	RecoveryTargetSelection = protocol.RecoveryTargetSelection
	RecoveryManifestEntry   = protocol.RecoveryManifestEntry
)

type Logger interface {
	Printf(format string, args ...interface{})
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	AgentID   string
	BaseURL   string
	AuthKey   string
	Client    HTTPDoer
	Logger    Logger
	UserAgent string
}

type Manager struct {
	cfg atomic.Value
}

type uploadTargetSummary struct {
	RecoveryTargetSelection
	ResolvedPaths []string `json:"resolvedPaths,omitempty"`
	TotalEntries  int      `json:"totalEntries,omitempty"`
	TotalBytes    int64    `json:"totalBytes,omitempty"`
}

const (
	maxPreviewBytes  = 4096
	previewSizeLimit = 1 << 20 // 1 MiB
)

func NewManager(cfg Config) *Manager {
	manager := &Manager{}
	manager.updateConfig(cfg)
	return manager
}

func (m *Manager) UpdateConfig(cfg Config) {
	if m == nil {
		return
	}
	m.updateConfig(cfg)
}

func (m *Manager) Shutdown() {}

func (m *Manager) updateConfig(cfg Config) {
	m.cfg.Store(cfg)
}

func (m *Manager) config() Config {
	if value := m.cfg.Load(); value != nil {
		if cfg, ok := value.(Config); ok {
			return cfg
		}
	}
	return Config{}
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{
		CommandID:   cmd.ID,
		CompletedAt: completedAt,
	}

	if len(cmd.Payload) == 0 {
		result.Success = false
		result.Error = "recovery payload missing"
		return result
	}

	var payload RecoveryCommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("invalid recovery payload: %v", err)
		return result
	}

	if strings.TrimSpace(payload.RequestID) == "" {
		payload.RequestID = cmd.ID
	}

	if len(payload.Selections) == 0 {
		result.Success = false
		result.Error = "no recovery selections provided"
		return result
	}

	cfg := m.config()
	if cfg.Client == nil {
		result.Success = false
		result.Error = "http client not configured"
		return result
	}

	archiveName := strings.TrimSpace(payload.ArchiveName)
	if archiveName == "" {
		archiveName = fmt.Sprintf("recovery-%s.zip", time.Now().UTC().Format("20060102T150405Z"))
	}
	if !strings.HasSuffix(strings.ToLower(archiveName), ".zip") {
		archiveName += ".zip"
	}

	data, manifest, summaries, err := m.packageSelections(ctx, payload)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	if ctx.Err() != nil {
		result.Success = false
		result.Error = "recovery interrupted"
		return result
	}

	if err := m.uploadArchive(ctx, cfg, payload, archiveName, data, manifest, summaries); err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.Output = fmt.Sprintf("uploaded %s with %d entries", archiveName, len(manifest))
	return result
}

func (m *Manager) userAgent() string {
	cfg := m.config()
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua != "" {
		return ua
	}
	return "tenvy-client"
}

func (m *Manager) logf(format string, args ...interface{}) {
	cfg := m.config()
	if cfg.Logger != nil {
		cfg.Logger.Printf(format, args...)
	}
}

func (m *Manager) packageSelections(
	ctx context.Context,
	payload RecoveryCommandPayload,
) ([]byte, []RecoveryManifestEntry, []uploadTargetSummary, error) {
	resolved := resolveSelections(payload.Selections)
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)
	var manifest []RecoveryManifestEntry
	summaries := make([]uploadTargetSummary, 0, len(resolved))
	seenArchivePaths := make(map[string]struct{})

	for _, item := range resolved {
		if ctx.Err() != nil {
			_ = zipWriter.Close()
			return nil, nil, nil, ctx.Err()
		}
		if len(item.paths) == 0 {
			continue
		}

		summary := uploadTargetSummary{RecoveryTargetSelection: item.selection}
		summary.ResolvedPaths = append(summary.ResolvedPaths, item.paths...)

		entriesBefore := len(manifest)
		var bytesCollected int64

		for _, path := range item.paths {
			if ctx.Err() != nil {
				_ = zipWriter.Close()
				return nil, nil, nil, ctx.Err()
			}
			count, size, err := m.addSourceToArchive(zipWriter, item.label, path, item.selection.Type, &manifest, seenArchivePaths)
			if err != nil {
				_ = zipWriter.Close()
				return nil, nil, nil, err
			}
			summary.TotalEntries += count
			bytesCollected += size
		}

		if summary.TotalEntries == 0 {
			manifest = manifest[:entriesBefore]
		} else {
			summary.TotalBytes = bytesCollected
		}
		summaries = append(summaries, summary)
	}

	if err := zipWriter.Close(); err != nil {
		return nil, nil, nil, err
	}

	if len(manifest) == 0 {
		return nil, nil, nil, fmt.Errorf("no artefacts collected for recovery request")
	}

	return buffer.Bytes(), manifest, summaries, nil
}

func (m *Manager) addSourceToArchive(
	zw *zip.Writer,
	archiveRoot string,
	sourcePath string,
	target string,
	manifest *[]RecoveryManifestEntry,
	seen map[string]struct{},
) (int, int64, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return 0, 0, nil
	}

	if info.IsDir() {
		return m.archiveDirectory(zw, archiveRoot, sourcePath, info, target, manifest, seen)
	}
	count, size, err := m.archiveFile(zw, archiveRoot, sourcePath, info, target, manifest, seen)
	return count, size, err
}

func (m *Manager) archiveDirectory(
	zw *zip.Writer,
	archiveRoot string,
	sourcePath string,
	info os.FileInfo,
	target string,
	manifest *[]RecoveryManifestEntry,
	seen map[string]struct{},
) (int, int64, error) {
	rootName := filepath.Base(sourcePath)
	rootName = sanitizeComponent(rootName)
	if rootName == "" {
		rootName = "root"
	}
	prefix := archiveJoin(archiveRoot, rootName)
	if _, ok := seen[prefix+"/"]; !ok {
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return 0, 0, err
		}
		header.Name = prefix + "/"
		if _, err := zw.CreateHeader(header); err != nil {
			return 0, 0, err
		}
		*manifest = append(*manifest, RecoveryManifestEntry{
			Path:       prefix + "/",
			Size:       0,
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
			Mode:       info.Mode().String(),
			Type:       "directory",
			Target:     target,
			SourcePath: sourcePath,
		})
		seen[prefix+"/"] = struct{}{}
	}

	var entries int
	var bytes int64
	err := filepath.WalkDir(sourcePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourcePath {
			return nil
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		archivePath := archiveJoin(prefix, filepath.ToSlash(rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			key := archivePath
			if !strings.HasSuffix(key, "/") {
				key += "/"
			}
			if _, exists := seen[key]; exists {
				return nil
			}
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			if !strings.HasSuffix(archivePath, "/") {
				archivePath += "/"
			}
			header.Name = archivePath
			if _, err := zw.CreateHeader(header); err != nil {
				return err
			}
			*manifest = append(*manifest, RecoveryManifestEntry{
				Path:       archivePath,
				Size:       0,
				ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
				Mode:       info.Mode().String(),
				Type:       "directory",
				Target:     target,
				SourcePath: path,
			})
			seen[archivePath] = struct{}{}
			return nil
		}
		count, size, err := m.archiveFile(zw, prefix, path, info, target, manifest, seen)
		if err != nil {
			return err
		}
		entries += count
		bytes += size
		return nil
	})
	if err != nil {
		return entries, bytes, err
	}
	return entries, bytes, nil
}

func (m *Manager) archiveFile(
	zw *zip.Writer,
	archiveRoot string,
	path string,
	info os.FileInfo,
	target string,
	manifest *[]RecoveryManifestEntry,
	seen map[string]struct{},
) (int, int64, error) {
	name := strings.TrimSpace(info.Name())
	if name == "" || name == "." || name == ".." {
		name = fmt.Sprintf("file-%d", time.Now().UnixNano())
	}
	candidate := archiveJoin(archiveRoot, name)
	if _, exists := seen[candidate]; exists {
		base := name
		ext := ""
		if idx := strings.LastIndex(name, "."); idx > 0 {
			base = name[:idx]
			ext = name[idx:]
		}
		suffix := 1
		for {
			altName := fmt.Sprintf("%s-%d%s", base, suffix, ext)
			candidate = archiveJoin(archiveRoot, altName)
			if _, exists := seen[candidate]; !exists {
				break
			}
			suffix++
		}
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return 0, 0, err
	}
	header.Name = candidate
	header.Method = zip.Deflate
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return 0, 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	_, err = io.Copy(writer, file)
	if err != nil {
		return 0, 0, err
	}

	preview, encoding, truncated := capturePreview(path, info)

	*manifest = append(*manifest, RecoveryManifestEntry{
		Path:            candidate,
		Size:            info.Size(),
		ModifiedAt:      info.ModTime().UTC().Format(time.RFC3339Nano),
		Mode:            info.Mode().String(),
		Type:            "file",
		Target:          target,
		SourcePath:      path,
		Preview:         preview,
		PreviewEncoding: encoding,
		Truncated:       truncated,
	})
	seen[candidate] = struct{}{}
	return 1, info.Size(), nil
}

func (m *Manager) uploadArchive(
	ctx context.Context,
	cfg Config,
	payload RecoveryCommandPayload,
	archiveName string,
	data []byte,
	manifest []RecoveryManifestEntry,
	summaries []uploadTargetSummary,
) error {
	endpoint := fmt.Sprintf("%s/api/agents/%s/recovery/upload", strings.TrimRight(cfg.BaseURL, "/"), url.PathEscape(cfg.AgentID))
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)

	if err := writer.WriteField("requestId", payload.RequestID); err != nil {
		return err
	}
	if err := writer.WriteField("archiveName", archiveName); err != nil {
		return err
	}
	if strings.TrimSpace(payload.Notes) != "" {
		if err := writer.WriteField("notes", payload.Notes); err != nil {
			return err
		}
	}
	if summariesJSON, err := json.Marshal(summaries); err == nil {
		_ = writer.WriteField("targets", string(summariesJSON))
	}
	if manifestJSON, err := json.Marshal(manifest); err == nil {
		_ = writer.WriteField("manifest", string(manifestJSON))
	}

	hash := sha256.Sum256(data)
	if err := writer.WriteField("sha256", hex.EncodeToString(hash[:])); err != nil {
		return err
	}
	if err := writer.WriteField("size", strconv.FormatInt(int64(len(data)), 10)); err != nil {
		return err
	}

	fileWriter, err := writer.CreateFormFile("archive", archiveName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fileWriter, bytes.NewReader(data)); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buffer.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", m.userAgent())
	if strings.TrimSpace(cfg.AuthKey) != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.AuthKey))
	}

	resp, err := cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = resp.Status
		}
		return fmt.Errorf("recovery upload failed: %s", detail)
	}

	m.logf("recovery archive %s uploaded (%d entries)", archiveName, len(manifest))
	return nil
}

func archiveJoin(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		cleaned = append(cleaned, part)
	}
	if len(cleaned) == 0 {
		return ""
	}
	return pathpkg.Join(cleaned...)
}

func capturePreview(path string, info os.FileInfo) (string, string, bool) {
	if info.IsDir() {
		return "", "", false
	}
	if info.Size() == 0 || info.Size() > previewSizeLimit {
		return "", "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer file.Close()
	buf := make([]byte, maxPreviewBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", "", false
	}
	data := buf[:n]
	truncated := int64(n) < info.Size()
	if looksLikeText(data) {
		return string(data), "utf-8", truncated
	}
	return base64.StdEncoding.EncodeToString(data), "base64", truncated
}

func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if !utf8.Valid(data) {
		return false
	}
	printable := 0
	for _, b := range data {
		switch {
		case b == '\n' || b == '\r' || b == '\t':
			printable++
		case b >= 0x20 && b < 0x7F:
			printable++
		case b >= 0x80:
			printable++
		default:
			return false
		}
	}
	return float64(printable)/float64(len(data)) >= 0.7
}

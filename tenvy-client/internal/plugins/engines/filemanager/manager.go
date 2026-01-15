package filemanagerengine

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command       = protocol.Command
	CommandResult = protocol.CommandResult
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

const (
	requestTimeout        = 10 * time.Second
	defaultDirectoryPerms = 0o755
	defaultFilePerms      = 0o644
	inlineTextLimit       = 128 * 1024
	streamChunkSize       = 512 * 1024
)

var errInvalidUTF8Content = errors.New("file content is not valid utf-8")

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

func (m *Manager) logf(format string, args ...interface{}) {
	cfg := m.config()
	if cfg.Logger == nil {
		return
	}
	cfg.Logger.Printf(format, args...)
}

func (m *Manager) userAgent() string {
	cfg := m.config()
	trimmed := strings.TrimSpace(cfg.UserAgent)
	if trimmed != "" {
		return trimmed
	}
	return "tenvy-client"
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	var payload FileManagerCommandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("invalid file manager payload: %v", err)
			return result
		}
	}

	action := strings.TrimSpace(payload.Action)
	if action == "" {
		result.Success = false
		result.Error = "file manager action is required"
		return result
	}

	var err error
	switch action {
	case "list-directory":
		err = m.handleListDirectory(ctx, payload, &result)
	case "read-file":
		err = m.handleReadFile(ctx, payload, &result)
	case "create-entry":
		err = m.handleCreateEntry(ctx, payload, &result)
	case "rename-entry":
		err = m.handleRenameEntry(ctx, payload, &result)
	case "move-entry":
		err = m.handleMoveEntry(ctx, payload, &result)
	case "delete-entry":
		err = m.handleDeleteEntry(ctx, payload, &result)
	case "update-file":
		err = m.handleUpdateFile(ctx, payload, &result)
	default:
		err = fmt.Errorf("unsupported file manager action: %s", action)
	}

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	result.Success = true
	return result
}

func (m *Manager) handleListDirectory(ctx context.Context, payload FileManagerCommandPayload, result *CommandResult) error {
	includeHidden := payload.IncludeHidden != nil && *payload.IncludeHidden
	directoryPath, err := resolvePath(payload.Path, true)
	if err != nil {
		return err
	}
	listing, err := buildDirectoryListing(directoryPath, includeHidden)
	if err != nil {
		return err
	}
	if err := m.dispatchResources(ctx, listing); err != nil {
		return err
	}
	result.Output = fmt.Sprintf("listed %s", listing.Path)
	return nil
}

func (m *Manager) handleReadFile(ctx context.Context, payload FileManagerCommandPayload, result *CommandResult) error {
	if strings.TrimSpace(payload.Path) == "" {
		return errors.New("file path is required")
	}
	filePath, err := resolvePath(payload.Path, false)
	if err != nil {
		return err
	}
	encoding := payload.Encoding
	contents, err := buildFileContent(filePath, encoding)
	if err != nil {
		return err
	}
	if len(contents) == 0 {
		return fmt.Errorf("no content produced for %s", filePath)
	}
	resources := make([]Resource, 0, len(contents))
	for _, content := range contents {
		resources = append(resources, content)
	}
	if err := m.dispatchResources(ctx, resources...); err != nil {
		return err
	}
	result.Output = fmt.Sprintf("read %s", contents[0].Path)
	return nil
}

func (m *Manager) handleCreateEntry(ctx context.Context, payload FileManagerCommandPayload, result *CommandResult) error {
	directory := strings.TrimSpace(payload.Directory)
	if directory == "" {
		return errors.New("target directory is required")
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return errors.New("entry name is required")
	}
	entryType := strings.TrimSpace(payload.EntryType)
	if entryType != "file" && entryType != "directory" {
		return fmt.Errorf("unsupported entry type: %s", entryType)
	}
	directoryPath, err := resolvePath(directory, false)
	if err != nil {
		return err
	}
	if err := ensureDirectoryExists(directoryPath); err != nil {
		return err
	}
	targetPath := filepath.Join(directoryPath, name)
	if _, err := os.Lstat(targetPath); err == nil {
		return fmt.Errorf("entry already exists: %s", targetPath)
	}

	switch entryType {
	case "file":
		data := []byte(payload.Content)
		if err := os.WriteFile(targetPath, data, defaultFilePerms); err != nil {
			return fmt.Errorf("create file: %w", err)
		}
	case "directory":
		if err := os.Mkdir(targetPath, defaultDirectoryPerms); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}

	resources := []Resource{}
	if listing, err := buildDirectoryListing(directoryPath, true); err == nil {
		resources = append(resources, listing)
	} else {
		m.logf("file-manager: failed to build directory listing for %s: %v", directoryPath, err)
	}
	if entryType == "file" {
		if contents, err := buildFileContent(targetPath, EncodingUTF8); err == nil {
			for _, content := range contents {
				resources = append(resources, content)
			}
		} else {
			m.logf("file-manager: failed to build file content for %s: %v", targetPath, err)
		}
	}
	if err := m.dispatchResources(ctx, resources...); err != nil {
		return err
	}
	result.Output = fmt.Sprintf("created %s", targetPath)
	return nil
}

func (m *Manager) handleRenameEntry(ctx context.Context, payload FileManagerCommandPayload, result *CommandResult) error {
	if strings.TrimSpace(payload.Path) == "" {
		return errors.New("entry path is required")
	}
	targetPath, err := resolvePath(payload.Path, false)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return errors.New("new entry name is required")
	}
	parentDir := filepath.Dir(targetPath)
	newPath := filepath.Join(parentDir, name)
	if _, err := os.Lstat(newPath); err == nil {
		return fmt.Errorf("target already exists: %s", newPath)
	}
	if err := os.Rename(targetPath, newPath); err != nil {
		return fmt.Errorf("rename entry: %w", err)
	}
	resources := []Resource{}
	if listing, err := buildDirectoryListing(parentDir, true); err == nil {
		resources = append(resources, listing)
	} else {
		m.logf("file-manager: failed to rebuild directory listing for %s: %v", parentDir, err)
	}
	if info, err := os.Lstat(newPath); err == nil && info.Mode().IsRegular() {
		if contents, err := buildFileContent(newPath, EncodingUTF8); err == nil {
			for _, content := range contents {
				resources = append(resources, content)
			}
		}
	}
	if err := m.dispatchResources(ctx, resources...); err != nil {
		return err
	}
	result.Output = fmt.Sprintf("renamed %s to %s", targetPath, newPath)
	return nil
}

func (m *Manager) handleMoveEntry(ctx context.Context, payload FileManagerCommandPayload, result *CommandResult) error {
	if strings.TrimSpace(payload.Path) == "" {
		return errors.New("entry path is required")
	}
	targetPath, err := resolvePath(payload.Path, false)
	if err != nil {
		return err
	}
	destination := strings.TrimSpace(payload.Destination)
	if destination == "" {
		return errors.New("destination directory is required")
	}
	destPath, err := resolvePath(destination, false)
	if err != nil {
		return err
	}
	if err := ensureDirectoryExists(destPath); err != nil {
		return err
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = filepath.Base(targetPath)
	}
	newPath := filepath.Join(destPath, name)
	if _, err := os.Lstat(newPath); err == nil {
		return fmt.Errorf("target already exists: %s", newPath)
	}
	if err := os.Rename(targetPath, newPath); err != nil {
		return fmt.Errorf("move entry: %w", err)
	}
	originDir := filepath.Dir(targetPath)
	resources := []Resource{}
	if listing, err := buildDirectoryListing(originDir, true); err == nil {
		resources = append(resources, listing)
	} else {
		m.logf("file-manager: failed to rebuild directory listing for %s: %v", originDir, err)
	}
	if destListing, err := buildDirectoryListing(destPath, true); err == nil {
		resources = append(resources, destListing)
	} else {
		m.logf("file-manager: failed to build directory listing for %s: %v", destPath, err)
	}
	if info, err := os.Lstat(newPath); err == nil && info.Mode().IsRegular() {
		if contents, err := buildFileContent(newPath, EncodingUTF8); err == nil {
			for _, content := range contents {
				resources = append(resources, content)
			}
		}
	}
	if err := m.dispatchResources(ctx, resources...); err != nil {
		return err
	}
	result.Output = fmt.Sprintf("moved %s to %s", targetPath, newPath)
	return nil
}

func (m *Manager) handleDeleteEntry(ctx context.Context, payload FileManagerCommandPayload, result *CommandResult) error {
	if strings.TrimSpace(payload.Path) == "" {
		return errors.New("entry path is required")
	}
	targetPath, err := resolvePath(payload.Path, false)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(targetPath)
	if err := os.RemoveAll(targetPath); err != nil {
		return fmt.Errorf("delete entry: %w", err)
	}
	resources := []Resource{}
	if listing, err := buildDirectoryListing(parentDir, true); err == nil {
		resources = append(resources, listing)
	} else {
		m.logf("file-manager: failed to rebuild directory listing for %s: %v", parentDir, err)
	}
	if err := m.dispatchResources(ctx, resources...); err != nil {
		return err
	}
	result.Output = fmt.Sprintf("deleted %s", targetPath)
	return nil
}

func (m *Manager) handleUpdateFile(ctx context.Context, payload FileManagerCommandPayload, result *CommandResult) error {
	if strings.TrimSpace(payload.Path) == "" {
		return errors.New("file path is required")
	}
	filePath, err := resolvePath(payload.Path, false)
	if err != nil {
		return err
	}
	encoding := payload.Encoding
	data, err := decodeFileContent(payload.Content, encoding)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filePath, data, defaultFilePerms); err != nil {
		return fmt.Errorf("update file: %w", err)
	}
	resources := []Resource{}
	if contents, err := buildFileContent(filePath, encoding); err == nil {
		for _, content := range contents {
			resources = append(resources, content)
		}
	} else {
		m.logf("file-manager: failed to rebuild file content for %s: %v", filePath, err)
	}
	if listing, err := buildDirectoryListing(filepath.Dir(filePath), true); err == nil {
		resources = append(resources, listing)
	} else {
		m.logf("file-manager: failed to rebuild directory listing for %s: %v", filepath.Dir(filePath), err)
	}
	if err := m.dispatchResources(ctx, resources...); err != nil {
		return err
	}
	result.Output = fmt.Sprintf("updated %s", filePath)
	return nil
}

func (m *Manager) dispatchResources(ctx context.Context, resources ...Resource) error {
	if len(resources) == 0 {
		return nil
	}
	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return errors.New("file-manager: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("file-manager: missing http client")
	}
	endpoint := fmt.Sprintf("%s/api/agents/%s/file-manager/state", baseURL, url.PathEscape(cfg.AgentID))
	payload := make(map[string]any)
	if len(resources) == 1 {
		payload["resource"] = resources[0]
	} else {
		payload["resources"] = resources
	}

	streamables := make([]*FileContent, 0)
	for _, resource := range resources {
		switch value := resource.(type) {
		case *FileContent:
			if value.Stream != nil {
				streamables = append(streamables, value)
			}
		case FileContent:
			if value.Stream != nil {
				copy := value
				streamables = append(streamables, &copy)
			}
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		closeStreamReaders(streamables)
		return err
	}

	reqCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) <= 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
	}

	var body io.Reader
	var contentType string
	var pipeReader *io.PipeReader

	if len(streamables) == 0 {
		body = bytes.NewReader(data)
		contentType = "application/json"
	} else {
		pr, pw := io.Pipe()
		pipeReader = pr
		multipartWriter := multipart.NewWriter(pw)

		go func() {
			defer closeStreamReaders(streamables)
			defer multipartWriter.Close()
			defer pw.Close()

			metaPart, err := multipartWriter.CreateFormField("metadata")
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := metaPart.Write(data); err != nil {
				pw.CloseWithError(err)
				return
			}
			for _, content := range streamables {
				descriptor := content.Stream
				if descriptor == nil {
					pw.CloseWithError(fmt.Errorf("file-manager: missing stream descriptor for %s", content.Path))
					return
				}
				reader := content.streamReader()
				if reader == nil {
					pw.CloseWithError(fmt.Errorf("file-manager: missing stream reader for %s", content.Path))
					return
				}
				header := textproto.MIMEHeader{}
				header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, descriptor.Part, content.Name))
				header.Set("Content-Type", "application/octet-stream")
				part, err := multipartWriter.CreatePart(header)
				if err != nil {
					pw.CloseWithError(err)
					return
				}
				if _, err := io.Copy(part, reader); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
		}()

		body = pipeReader
		contentType = multipartWriter.FormDataContentType()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, body)
	if err != nil {
		if pipeReader != nil {
			pipeReader.CloseWithError(err)
		}
		closeStreamReaders(streamables)
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(m.userAgent()); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(cfg.AuthKey); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}
	resp, err := cfg.Client.Do(req)
	if err != nil {
		if pipeReader != nil {
			pipeReader.CloseWithError(err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return fmt.Errorf("file-manager state upload failed: %s", message)
	}
	return nil
}

func resolvePath(raw string, allowEmpty bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		if allowEmpty {
			return defaultDirectory(), nil
		}
		return "", errors.New("path is required")
	}
	expanded, err := expandHome(trimmed)
	if err != nil {
		return "", err
	}
	normalized := filepath.Clean(convertSeparators(expanded))
	if runtime.GOOS == "windows" && len(normalized) == 2 && normalized[1] == ':' {
		normalized += "\\"
	}
	if !isAbsolutePath(normalized) {
		abs, err := filepath.Abs(normalized)
		if err != nil {
			return "", err
		}
		normalized = convertSeparators(abs)
	}
	return normalized, nil
}

func ensureDirectoryExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", path)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return nil
}

func convertSeparators(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(path, "/", "\\")
	}
	return strings.ReplaceAll(path, "\\", "/")
}

func isAbsolutePath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(path, "\\") {
			return true
		}
		if len(path) >= 2 && path[1] == ':' {
			return true
		}
	}
	return false
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func defaultDirectory() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return convertSeparators(home)
	}
	if runtime.GOOS == "windows" {
		drive := strings.TrimSpace(os.Getenv("SystemDrive"))
		if drive == "" {
			drive = "C:"
		}
		if !strings.HasSuffix(drive, "\\") {
			drive += "\\"
		}
		return drive
	}
	return "/"
}

func buildDirectoryListing(path string, includeHidden bool) (DirectoryListing, error) {
	info, err := os.Stat(path)
	if err != nil {
		return DirectoryListing{}, err
	}
	if !info.IsDir() {
		return DirectoryListing{}, fmt.Errorf("not a directory: %s", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return DirectoryListing{}, err
	}
	normalizedPath := normalizePath(path)
	listing := DirectoryListing{
		Type:    "directory",
		Root:    deriveRoot(normalizedPath),
		Path:    normalizedPath,
		Parent:  parentDirectory(normalizedPath),
		Entries: make([]FileSystemEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		entryInfo, err := entry.Info()
		if err != nil {
			continue
		}
		entryPath := normalizePath(filepath.Join(path, entry.Name()))
		hidden := isHiddenFile(entryInfo, entryPath)
		if hidden && !includeHidden {
			continue
		}
		entryType := determineEntryType(entryInfo)
		var size *int64
		if entryType == EntryTypeFile {
			value := entryInfo.Size()
			size = &value
		}
		listing.Entries = append(listing.Entries, FileSystemEntry{
			Name:       entry.Name(),
			Path:       entryPath,
			Type:       entryType,
			Size:       size,
			ModifiedAt: entryInfo.ModTime().UTC().Format(time.RFC3339Nano),
			IsHidden:   hidden,
		})
	}
	sort.Slice(listing.Entries, func(i, j int) bool {
		a := strings.ToLower(listing.Entries[i].Name)
		b := strings.ToLower(listing.Entries[j].Name)
		if a == b {
			return listing.Entries[i].Name < listing.Entries[j].Name
		}
		return a < b
	})
	return listing, nil
}

func buildFileContent(path string, preferredEncoding FileEncoding) ([]*FileContent, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", path)
	}

	normalizedPath := normalizePath(path)
	base := FileContent{
		Type:       "file",
		Root:       deriveRoot(normalizedPath),
		Path:       normalizedPath,
		Name:       filepath.Base(normalizedPath),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
	}

	encoding := preferredEncoding
	if encoding == "" {
		encoding = EncodingUTF8
	}

	if encoding == EncodingUTF8 {
		if info.Size() <= inlineTextLimit {
			content, inlineErr := readUTF8Content(path, info.Size())
			if inlineErr == nil {
				inline := base
				inline.Encoding = EncodingUTF8
				inline.Content = content
				return []*FileContent{&inline}, nil
			}
			if errors.Is(inlineErr, errInvalidUTF8Content) {
				encoding = EncodingBase64
			} else if inlineErr != nil {
				return nil, inlineErr
			}
		} else {
			encoding = EncodingBase64
		}
	}

	if encoding != EncodingBase64 {
		return nil, fmt.Errorf("unsupported file encoding: %s", encoding)
	}

	return buildStreamedFileContents(path, base, info)
}

func readUTF8Content(path string, size int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buf bytes.Buffer
	if size > 0 && size <= inlineTextLimit {
		buf.Grow(int(size))
	}
	if _, err := io.CopyN(&buf, file, size); err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", err
		}
	}
	data := buf.Bytes()
	if !shouldUseTextEncoding(data) {
		return "", errInvalidUTF8Content
	}
	return buf.String(), nil
}

func buildStreamedFileContents(path string, base FileContent, info fs.FileInfo) ([]*FileContent, error) {
	size := info.Size()
	chunkCount := int((size + int64(streamChunkSize) - 1) / int64(streamChunkSize))
	if chunkCount == 0 {
		chunkCount = 1
	}
	streamID := generateStreamID(base.Path, info.ModTime(), size)
	resources := make([]*FileContent, 0, chunkCount)
	for i := 0; i < chunkCount; i++ {
		offset := int64(i) * int64(streamChunkSize)
		length := int64(streamChunkSize)
		remaining := size - offset
		if remaining < int64(streamChunkSize) {
			length = remaining
		}
		reader, err := newFileChunkReader(path, offset, length)
		if err != nil {
			closeStreamReaders(resources)
			return nil, err
		}
		chunk := base
		chunk.Encoding = EncodingBase64
		chunk.Stream = &FileStream{
			ID:     streamID,
			Part:   fmt.Sprintf("chunk-%s-%d", streamID, i),
			Index:  i,
			Count:  chunkCount,
			Offset: offset,
			Length: length,
		}
		chunk.setStreamReader(reader)
		resources = append(resources, &chunk)
	}
	return resources, nil
}

func closeStreamReaders(resources []*FileContent) {
	for _, resource := range resources {
		if reader := resource.streamReader(); reader != nil {
			reader.Close()
		}
	}
}

func generateStreamID(path string, modTime time.Time, size int64) string {
	hash := sha1.New()
	hash.Write([]byte(strings.ToLower(path)))
	hash.Write([]byte{0})
	hash.Write([]byte(modTime.UTC().Format(time.RFC3339Nano)))
	hash.Write([]byte{0})
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(size))
	hash.Write(length[:])
	return hex.EncodeToString(hash.Sum(nil))
}

func newFileChunkReader(path string, offset, length int64) (io.ReadCloser, error) {
	if length == 0 {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return nil, err
	}
	return &fileChunkReader{file: file, reader: io.LimitReader(file, length)}, nil
}

type fileChunkReader struct {
	file   *os.File
	reader io.Reader
}

func (r *fileChunkReader) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *fileChunkReader) Close() error {
	return r.file.Close()
}

func determineEntryType(info fs.FileInfo) FileSystemEntryType {
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		return EntryTypeSymlink
	case mode.IsDir():
		return EntryTypeDir
	case mode.IsRegular():
		return EntryTypeFile
	default:
		return EntryTypeOther
	}
}

func shouldUseTextEncoding(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	if !utf8.Valid(sample) {
		return false
	}
	for _, b := range sample {
		if b == 0 {
			return false
		}
	}
	return true
}

func decodeFileContent(content string, encoding FileEncoding) ([]byte, error) {
	switch encoding {
	case "":
		fallthrough
	case EncodingUTF8:
		return []byte(content), nil
	case EncodingBase64:
		return base64.StdEncoding.DecodeString(content)
	default:
		return nil, fmt.Errorf("unsupported file encoding: %s", encoding)
	}
}

func deriveRoot(path string) string {
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(path)
		if volume != "" {
			volume = convertSeparators(volume)
			if strings.HasPrefix(volume, "\\") {
				return volume
			}
			if !strings.HasSuffix(volume, "\\") {
				return volume + "\\"
			}
			return volume
		}
	}
	return "/"
}

func parentDirectory(path string) *string {
	parent := filepath.Dir(path)
	parent = normalizePath(parent)
	if parent == path {
		return nil
	}
	if parent == "." {
		return nil
	}
	value := parent
	return &value
}

func normalizePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return trimmed
	}
	normalized := filepath.Clean(convertSeparators(trimmed))
	if runtime.GOOS == "windows" && len(normalized) == 2 && normalized[1] == ':' {
		normalized += "\\"
	}
	return normalized
}

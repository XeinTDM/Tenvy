package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

const statusFileName = ".status.json"

type installationStatusRecord struct {
	ID        string                       `json:"pluginId,omitempty"`
	Version   string                       `json:"version,omitempty"`
	Status    manifest.PluginInstallStatus `json:"status,omitempty"`
	Error     string                       `json:"error,omitempty"`
	Timestamp *int64                       `json:"timestamp,omitempty"`
}

func (r *installationStatusRecord) UnmarshalJSON(data []byte) error {
	type rawRecord struct {
		ID        string      `json:"pluginId,omitempty"`
		Version   string      `json:"version,omitempty"`
		Status    string      `json:"status,omitempty"`
		Error     string      `json:"error,omitempty"`
		Timestamp interface{} `json:"timestamp,omitempty"`
	}

	var raw rawRecord
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	r.ID = raw.ID
	r.Version = raw.Version
	r.Status = manifest.PluginInstallStatus(raw.Status)
	r.Error = raw.Error
	r.Timestamp = parseStatusTimestamp(raw.Timestamp)
	return nil
}

func parseStatusTimestamp(value interface{}) *int64 {
	switch ts := value.(type) {
	case nil:
		return nil
	case float64:
		millis := int64(ts)
		return &millis
	case int64:
		millis := ts
		return &millis
	case json.Number:
		if v, err := ts.Int64(); err == nil {
			millis := v
			return &millis
		}
	case string:
		trimmed := strings.TrimSpace(ts)
		if trimmed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			millis := parsed.UTC().UnixMilli()
			return &millis
		}
		if numeric, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			millis := numeric
			return &millis
		}
	}
	return nil
}

func normalizeInstallStatus(status manifest.PluginInstallStatus) manifest.PluginInstallStatus {
	switch strings.ToLower(strings.TrimSpace(string(status))) {
	case string(manifest.InstallInstalled):
		return manifest.InstallInstalled
	case string(manifest.InstallBlocked):
		return manifest.InstallBlocked
	case string(manifest.InstallDisabled):
		return manifest.InstallDisabled
	case string(manifest.InstallError), "failed", "pending", "installing":
		return manifest.InstallError
	default:
		return manifest.InstallError
	}
}

func (r *installationStatusRecord) PluginID(defaultID string) string {
	if r == nil {
		return strings.TrimSpace(defaultID)
	}
	if id := strings.TrimSpace(r.ID); id != "" {
		return id
	}
	return strings.TrimSpace(defaultID)
}

func loadInstallationStatus(dir string) *installationStatusRecord {
	path := filepath.Join(dir, statusFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var record installationStatusRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil
	}
	record.Status = normalizeInstallStatus(record.Status)
	return &record
}

func writeInstallationStatus(dir string, record installationStatusRecord) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("plugin status directory not provided")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure plugin status directory: %w", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal installation status: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "status-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary status file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write status data: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close status file: %w", err)
	}
	target := filepath.Join(dir, statusFileName)
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("persist status file: %w", err)
	}
	return nil
}

func removeInstallationStatus(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("plugin status directory not provided")
	}
	path := filepath.Join(dir, statusFileName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove status file: %w", err)
	}
	return nil
}

func (m *Manager) recordInstallStatusLocked(pluginID, version string, status manifest.PluginInstallStatus, message string) error {
	if m == nil {
		return errors.New("plugins manager is nil")
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return errors.New("plugin id not provided")
	}
	dir := filepath.Join(m.root, pluginID)
	timestamp := time.Now().UTC().UnixMilli()
	record := installationStatusRecord{
		ID:        pluginID,
		Version:   strings.TrimSpace(version),
		Status:    normalizeInstallStatus(status),
		Error:     strings.TrimSpace(message),
		Timestamp: &timestamp,
	}
	return writeInstallationStatus(dir, record)
}

func (m *Manager) clearInstallStatusLocked(pluginID string) error {
	if m == nil {
		return nil
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return nil
	}
	dir := filepath.Join(m.root, pluginID)
	return removeInstallationStatus(dir)
}

func RecordInstallStatus(m *Manager, pluginID, version string, status manifest.PluginInstallStatus, message string) error {
	if m == nil {
		return errors.New("plugins manager not initialized")
	}
	m.stageMu.Lock()
	defer m.stageMu.Unlock()
	return m.recordInstallStatusLocked(pluginID, version, status, message)
}

func ClearInstallStatus(m *Manager, pluginID string) error {
	if m == nil {
		return nil
	}
	m.stageMu.Lock()
	defer m.stageMu.Unlock()
	return m.clearInstallStatusLocked(pluginID)
}

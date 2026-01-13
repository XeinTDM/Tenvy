package webcam

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

type framePacket struct {
	Data       []byte
	MimeType   string
	CapturedAt time.Time
	Err        error
}

type frameSource interface {
	Start(ctx context.Context) (<-chan framePacket, error)
	ApplySettings(settings *protocol.WebcamStreamSettings) error
	Close() error
}

type frameSourceFactory func(deviceID string, settings *protocol.WebcamStreamSettings) (frameSource, error)

type Manager struct {
	cfg          atomic.Value
	mu           sync.Mutex
	sessions     map[string]*streamSession
	frameFactory frameSourceFactory
	now          func() time.Time
}

func NewManager(cfg Config) *Manager {
	manager := &Manager{
		sessions:     make(map[string]*streamSession),
		frameFactory: defaultFrameSourceFactory,
		now:          time.Now,
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
		return value.(Config)
	}
	return Config{}
}

func (m *Manager) currentTime() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
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
	if m == nil {
		return
	}
	cfg := m.config()
	if cfg.Logger == nil {
		return
	}
	cfg.Logger.Printf(format, args...)
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := m.currentTime().UTC().Format(time.RFC3339Nano)

	var payload protocol.WebcamCommandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return CommandResult{
				CommandID:   cmd.ID,
				Success:     false,
				Error:       fmt.Sprintf("invalid webcam control payload: %v", err),
				CompletedAt: completedAt,
			}
		}
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	var err error

	switch action {
	case "enumerate", "inventory":
		err = m.publishInventory(ctx, payload.RequestID)
	case "start":
		err = m.startSession(ctx, payload)
	case "stop":
		err = m.stopSession(strings.TrimSpace(payload.SessionID))
	case "update":
		err = m.updateSession(ctx, payload)
	case "":
		err = errors.New("missing webcam control action")
	default:
		err = fmt.Errorf("unsupported webcam control action: %s", payload.Action)
	}

	result := CommandResult{CommandID: cmd.ID, CompletedAt: m.currentTime().UTC().Format(time.RFC3339Nano)}
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	result.Success = true
	return result
}

func (m *Manager) publishInventory(ctx context.Context, requestID string) error {
	devices, warning, err := captureWebcamInventory()
	if err != nil {
		return err
	}

	inventory := protocol.WebcamDeviceInventory{
		Devices:    devices,
		CapturedAt: m.currentTime().UTC().Format(time.RFC3339Nano),
	}
	if trimmed := strings.TrimSpace(requestID); trimmed != "" {
		inventory.RequestID = trimmed
	}
	if warning != "" {
		inventory.Warning = warning
	}

	data, err := json.Marshal(inventory)
	if err != nil {
		return err
	}

	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return errors.New("webcam control: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("webcam control: missing http client")
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/webcam/devices", baseURL, url.PathEscape(cfg.AgentID))
	reqCtx, cancel := m.requestContext(ctx)
	defer cancel()

	resp, err := m.doJSONRequest(reqCtx, http.MethodPost, endpoint, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webcam inventory publish failed: status %d", resp.StatusCode)
	}

	return nil
}

func (m *Manager) startSession(ctx context.Context, payload protocol.WebcamCommandPayload) error {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return errors.New("webcam session identifier is required")
	}
	deviceID := strings.TrimSpace(payload.DeviceID)
	if deviceID == "" {
		return errors.New("webcam device identifier is required")
	}

	cfg := m.config()
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("webcam control: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("webcam control: missing http client")
	}

	factory := m.frameFactory
	if factory == nil {
		return errors.New("webcam capture factory is not configured")
	}

	source, err := factory(deviceID, cloneStreamSettings(payload.Settings))
	if err != nil {
		return err
	}

	session := &streamSession{
		manager:     m,
		id:          sessionID,
		deviceID:    deviceID,
		source:      source,
		settings:    cloneStreamSettings(payload.Settings),
		negotiation: cloneNegotiationState(payload.Negotiation),
		done:        make(chan struct{}),
	}

	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = make(map[string]*streamSession)
	}
	if existing := m.sessions[sessionID]; existing != nil {
		delete(m.sessions, sessionID)
		m.mu.Unlock()
		existing.requestStop.Store(true)
		existing.stop()
		if !existing.wait(15 * time.Second) {
			m.logf("webcam stream %s previous session did not stop within timeout", sessionID)
		}
	} else {
		m.mu.Unlock()
	}

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	go session.run()
	return nil
}

func (m *Manager) stopSession(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("webcam session identifier is required")
	}

	m.mu.Lock()
	session := m.sessions[sessionID]
	if session != nil {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if session == nil {
		return nil
	}

	session.requestStop.Store(true)
	session.stop()
	if !session.wait(5 * time.Second) {
		return errors.New("timeout waiting for webcam session to stop")
	}
	return nil
}

func (m *Manager) updateSession(ctx context.Context, payload protocol.WebcamCommandPayload) error {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return errors.New("webcam session identifier is required")
	}

	m.mu.Lock()
	session := m.sessions[sessionID]
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("webcam session %s is not active", sessionID)
	}

	if payload.Settings != nil {
		if err := session.applySettings(payload.Settings); err != nil {
			return err
		}
	}

	patch := make(map[string]any)
	if payload.Negotiation != nil {
		patch["negotiation"] = payload.Negotiation
	}
	if len(patch) == 0 {
		return nil
	}

	return m.patchSession(ctx, sessionID, patch)
}

func (m *Manager) patchSession(ctx context.Context, sessionID string, patch map[string]any) error {
	if len(patch) == 0 {
		return nil
	}
	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return errors.New("webcam control: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("webcam control: missing http client")
	}

	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/webcam/sessions/%s", baseURL, url.PathEscape(cfg.AgentID), url.PathEscape(sessionID))
	reqCtx, cancel := m.requestContext(ctx)
	defer cancel()

	resp, err := m.doJSONRequest(reqCtx, http.MethodPatch, endpoint, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webcam session update failed: status %d", resp.StatusCode)
	}
	return nil
}

func (m *Manager) sendFrame(ctx context.Context, sessionID string, packet framePacket) error {
	cfg := m.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return errors.New("webcam control: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("webcam control: missing http client")
	}

	capturedAt := packet.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = m.currentTime()
	}

	mimeType := strings.TrimSpace(packet.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	payload := map[string]any{
		"capturedAt": capturedAt.UTC().Format(time.RFC3339Nano),
		"mimeType":   mimeType,
		"data":       base64.StdEncoding.EncodeToString(packet.Data),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/webcam/sessions/%s/frames", baseURL, url.PathEscape(cfg.AgentID), url.PathEscape(sessionID))
	reqCtx, cancel := m.requestContext(ctx)
	defer cancel()

	resp, err := m.doJSONRequest(reqCtx, http.MethodPost, endpoint, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webcam frame publish failed: status %d", resp.StatusCode)
	}
	return nil
}

func (m *Manager) doJSONRequest(ctx context.Context, method, endpoint string, body []byte) (*http.Response, error) {
	cfg := m.config()
	if cfg.Client == nil {
		return nil, errors.New("webcam control: missing http client")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(m.userAgent()); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(cfg.AuthKey); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}
	return cfg.Client.Do(req)
}

func (m *Manager) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 10*time.Second)
}

func (m *Manager) setFrameSourceFactory(factory frameSourceFactory) {
	if m == nil || factory == nil {
		return
	}
	m.mu.Lock()
	m.frameFactory = factory
	m.mu.Unlock()
}

func (m *Manager) setNowFunc(now func() time.Time) {
	if m == nil || now == nil {
		return
	}
	m.mu.Lock()
	m.now = now
	m.mu.Unlock()
}

type streamSession struct {
	manager     *Manager
	id          string
	deviceID    string
	source      frameSource
	settings    *protocol.WebcamStreamSettings
	negotiation *protocol.WebcamNegotiationState
	done        chan struct{}

	cancelMu sync.Mutex
	cancel   context.CancelFunc

	closeOnce   sync.Once
	requestStop atomic.Bool
}

func (s *streamSession) run() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelMu.Lock()
	s.cancel = cancel
	s.cancelMu.Unlock()

	defer func() {
		cancel()
		s.closeSource()
		s.manager.mu.Lock()
		if current := s.manager.sessions[s.id]; current == s {
			delete(s.manager.sessions, s.id)
		}
		s.manager.mu.Unlock()
		close(s.done)
	}()

	frames, err := s.source.Start(ctx)
	if err != nil {
		s.manager.logf("webcam stream %s failed to start: %v", s.id, err)
		s.manager.patchSession(context.Background(), s.id, map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}

	patch := map[string]any{"status": "active"}
	if s.negotiation != nil {
		patch["negotiation"] = s.negotiation
	}
	if err := s.manager.patchSession(context.Background(), s.id, patch); err != nil {
		s.manager.logf("webcam stream %s failed to report active state: %v", s.id, err)
	}

	for {
		select {
		case <-ctx.Done():
			if s.requestStop.Load() {
				if err := s.manager.patchSession(context.Background(), s.id, map[string]any{"status": "stopped"}); err != nil {
					s.manager.logf("webcam stream %s stop patch failed: %v", s.id, err)
				}
			}
			return
		case packet, ok := <-frames:
			if !ok {
				if err := s.manager.patchSession(context.Background(), s.id, map[string]any{"status": "stopped"}); err != nil {
					s.manager.logf("webcam stream %s completion patch failed: %v", s.id, err)
				}
				return
			}
			if packet.Err != nil {
				s.manager.patchSession(context.Background(), s.id, map[string]any{
					"status": "error",
					"error":  packet.Err.Error(),
				})
				return
			}
			if len(packet.Data) == 0 {
				continue
			}
			if err := s.manager.sendFrame(context.Background(), s.id, packet); err != nil {
				s.manager.logf("webcam stream %s frame delivery failed: %v", s.id, err)
				s.manager.patchSession(context.Background(), s.id, map[string]any{
					"status": "error",
					"error":  err.Error(),
				})
				return
			}
		}
	}
}

func (s *streamSession) stop() {
	if s == nil {
		return
	}
	s.cancelMu.Lock()
	cancel := s.cancel
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.closeSource()
}

func (s *streamSession) wait(timeout time.Duration) bool {
	if s == nil {
		return true
	}
	if timeout <= 0 {
		<-s.done
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.done:
		return true
	case <-timer.C:
		return false
	}
}

func (s *streamSession) closeSource() {
	s.closeOnce.Do(func() {
		if s.source != nil {
			if err := s.source.Close(); err != nil {
				s.manager.logf("webcam stream %s close failed: %v", s.id, err)
			}
		}
	})
}

func (s *streamSession) applySettings(settings *protocol.WebcamStreamSettings) error {
	if s == nil {
		return errors.New("webcam session is not available")
	}
	if err := s.source.ApplySettings(settings); err != nil {
		return err
	}
	s.settings = cloneStreamSettings(settings)
	return nil
}

func cloneStreamSettings(input *protocol.WebcamStreamSettings) *protocol.WebcamStreamSettings {
	if input == nil {
		return nil
	}
	clone := *input
	return &clone
}

func cloneNegotiationState(input *protocol.WebcamNegotiationState) *protocol.WebcamNegotiationState {
	if input == nil {
		return nil
	}
	clone := &protocol.WebcamNegotiationState{}
	if input.Offer != nil {
		offer := *input.Offer
		if offer.IceServers != nil {
			offer.IceServers = append([]string(nil), offer.IceServers...)
		}
		clone.Offer = &offer
	}
	if input.Answer != nil {
		answer := *input.Answer
		if answer.IceServers != nil {
			answer.IceServers = append([]string(nil), answer.IceServers...)
		}
		clone.Answer = &answer
	}
	return clone
}

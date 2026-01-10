//go:build cgo && !tenvy_no_audio
// +build cgo,!tenvy_no_audio

package audio

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/rootbay/tenvy-client/internal/protocol"
	"nhooyr.io/websocket"
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

type AudioBridge struct {
	cfg       atomic.Value // stores Config
	mu        sync.Mutex
	sessions  map[string]*AudioStreamSession
	playbacks map[string]*playbackSession
}

type playbackSession struct {
	bridge  *AudioBridge
	id      string
	trackID string
	url     string
	volume  float64
	loop    bool

	ctx    *malgo.AllocatedContext
	device *malgo.Device

	pcmData    []byte
	format     malgo.Format
	channels   uint32
	sampleRate uint32

	offset  uint32
	paused  atomic.Bool
	stopped  atomic.Bool
	done    chan struct{}
}

func (s *playbackSession) stop() {
	if s == nil {
		return
	}
	s.stopped.Store(true)
	if s.device != nil {
		_ = s.device.Stop()
		s.device.Uninit()
	}
	if s.ctx != nil {
		_ = s.ctx.Context.Uninit()
		s.ctx.Free()
	}
}

type AudioStreamSession struct {
	bridge     *AudioBridge
	id         string
	deviceID   string
	deviceName string
	direction  AudioDirection
	format     AudioStreamFormat

	ctx    *malgo.AllocatedContext
	device *malgo.Device

	buffers   chan []byte
	runCtx    context.Context
	runCancel context.CancelFunc
	stopped   atomic.Bool
	sequence  uint64

	transportMu  sync.Mutex
	transport    *AudioStreamTransport
	transportURL string
	streamConn   *websocket.Conn

	deviceToken malgo.DeviceID
	useDeviceID bool
	done        chan struct{}
}

type audioBinaryHeader struct {
	SessionID string            `json:"sessionId" msgpack:"sessionId"`
	Sequence  uint64            `json:"sequence" msgpack:"sequence"`
	Timestamp string            `json:"timestamp" msgpack:"timestamp"`
	Format    AudioStreamFormat `json:"format" msgpack:"format"`
}

func (s *AudioStreamSession) logf(format string, args ...interface{}) {
	if s == nil || s.bridge == nil {
		return
	}
	s.bridge.logf(format, args...)
}

func NewAudioBridge(cfg Config) *AudioBridge {
	bridge := &AudioBridge{
		sessions:  make(map[string]*AudioStreamSession),
		playbacks: make(map[string]*playbackSession),
	}
	bridge.updateConfig(cfg)
	return bridge
}

func (b *AudioBridge) UpdateConfig(cfg Config) {
	if b == nil {
		return
	}
	b.updateConfig(cfg)
}

func (b *AudioBridge) updateConfig(cfg Config) {
	b.cfg.Store(cfg)
}

func (b *AudioBridge) config() Config {
	if value := b.cfg.Load(); value != nil {
		return value.(Config)
	}
	return Config{}
}

func (b *AudioBridge) logf(format string, args ...interface{}) {
	cfg := b.config()
	if cfg.Logger == nil {
		return
	}
	cfg.Logger.Printf(format, args...)
}

func (b *AudioBridge) userAgent() string {
	ua := strings.TrimSpace(b.config().UserAgent)
	if ua != "" {
		return ua
	}
	return "tenvy-client"
}

func (b *AudioBridge) Shutdown() {
	b.mu.Lock()
	sessions := make([]*AudioStreamSession, 0, len(b.sessions))
	for _, session := range b.sessions {
		sessions = append(sessions, session)
	}
	b.sessions = make(map[string]*AudioStreamSession)

	playbacks := make([]*playbackSession, 0, len(b.playbacks))
	for _, p := range b.playbacks {
		playbacks = append(playbacks, p)
	}
	b.playbacks = make(map[string]*playbackSession)
	b.mu.Unlock()

	for _, session := range sessions {
		session.stop()
		session.wait(2 * time.Second)
	}

	for _, p := range playbacks {
		p.stop()
	}
}

func (b *AudioBridge) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)

	var payload AudioControlCommandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return CommandResult{
				CommandID:   cmd.ID,
				Success:     false,
				Error:       fmt.Sprintf("invalid audio control payload: %v", err),
				CompletedAt: completedAt,
			}
		}
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	var err error
	switch action {
	case "enumerate", "inventory":
		err = b.publishInventory(ctx, payload.RequestID)
	case "start":
		err = b.startSession(ctx, payload)
	case "stop":
		err = b.stopSession(payload.SessionID)
	case "playback-start":
		err = b.startPlayback(ctx, payload)
	case "playback-pause":
		err = b.pausePlayback(payload.TrackID)
	case "playback-resume":
		err = b.resumePlayback(payload.TrackID)
	case "playback-stop":
		err = b.stopPlayback(payload.TrackID)
	case "":
		err = errors.New("missing audio control action")
	default:
		err = fmt.Errorf("unsupported audio control action: %s", payload.Action)
	}

	if err != nil {
		return CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       err.Error(),
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}

	return CommandResult{
		CommandID:   cmd.ID,
		Success:     true,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func (b *AudioBridge) publishInventory(ctx context.Context, requestID string) error {
	inventory, err := captureAudioInventory()
	if err != nil {
		return err
	}
	inventory.RequestID = requestID

	data, err := json.Marshal(inventory)
	if err != nil {
		return err
	}

	cfg := b.config()

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return errors.New("audio control: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("audio control: missing http client")
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/audio/devices", baseURL, url.PathEscape(cfg.AgentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(b.userAgent()); ua != "" {
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
		return fmt.Errorf("inventory publish failed: status %d", resp.StatusCode)
	}
	return nil
}

func (b *AudioBridge) startSession(ctx context.Context, payload AudioControlCommandPayload) error {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return errors.New("audio session identifier is required")
	}

	direction := payload.Direction
	if direction == "" {
		direction = AudioDirectionInput
	}

	channels := payload.Channels
	if channels <= 0 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	sampleRate := payload.SampleRate
	if sampleRate <= 0 {
		sampleRate = 48000
	}

	encoding := strings.TrimSpace(payload.Encoding)
	if encoding == "" {
		encoding = "pcm16"
	}
	if encoding != "pcm16" {
		return fmt.Errorf("unsupported audio encoding: %s", encoding)
	}

	cfg := b.config()

	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("audio control: missing base URL")
	}
	if cfg.Client == nil {
		return errors.New("audio control: missing http client")
	}

	session := &AudioStreamSession{
		bridge:     b,
		id:         sessionID,
		deviceID:   strings.TrimSpace(payload.DeviceID),
		deviceName: strings.TrimSpace(payload.DeviceLabel),
		direction:  direction,
		format: AudioStreamFormat{
			Encoding:   encoding,
			SampleRate: sampleRate,
			Channels:   channels,
		},
		buffers: make(chan []byte, 32),
		done:    make(chan struct{}),
	}

	b.mu.Lock()
	if existing, ok := b.sessions[sessionID]; ok {
		b.mu.Unlock()
		existing.stop()
		existing.wait(2 * time.Second)
	} else if len(b.sessions) > 0 {
		for _, active := range b.sessions {
			actve.stop()
			go active.wait(2 * time.Second)
		}
		b.sessions = make(map[string]*AudioStreamSession)
		b.mu.Unlock()
	} else {
		b.mu.Unlock()
	}

	allocatedCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize audio context: %w", err)
	}

	deviceType := malgo.Capture
	if direction == AudioDirectionOutput {
		deviceType = malgo.Loopback
	}

	deviceConfig := malgo.DefaultDeviceConfig(deviceType)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(channels)
	deviceConfig.SampleRate = uint32(sampleRate)
	deviceConfig.Alsa.NoMMap = 1
	deviceConfig.Capture.ShareMode = malgo.Shared

	if session.deviceID != "" {
		token, err := parseDeviceID(session.deviceID)
		if err != nil {
			allocatedCtx.Context.Uninit()
			allocatedCtx.Free()
			return fmt.Errorf("invalid device identifier: %w", err)
		}
		session.deviceToken = token
		session.useDeviceID = true
		deviceConfig.Capture.DeviceID = unsafe.Pointer(&session.deviceToken)
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(_ []byte, input []byte, _ uint32) {
			session.handleInput(input)
		},
		Stop: func() {
			session.stopped.Store(true)
		},
	}

	device, err := malgo.InitDevice(allocatedCtx.Context, deviceConfig, callbacks)
	if err != nil {
		allocatedCtx.Context.Uninit()
		allocatedCtx.Free()
		return fmt.Errorf("failed to initialize capture device: %w", err)
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		allocatedCtx.Context.Uninit()
		allocatedCtx.Free()
		return fmt.Errorf("failed to start capture device: %w", err)
	}

	session.ctx = allocatedCtx
	session.device = device
	session.runCtx, session.runCancel = context.WithCancel(context.Background())

	if err := session.configureTransport(cfg, payload.StreamTransport); err != nil {
		session.logf("audio stream %s transport negotiation failed: %v", session.id, err)
	}

	go session.run()

	b.mu.Lock()
	b.sessions[session.id] = session
	b.mu.Unlock()

	return nil
}

func (b *AudioBridge) stopSession(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("audio session identifier is required")
	}

	b.mu.Lock()
	session, ok := b.sessions[sessionID]
	if ok {
		delete(b.sessions, sessionID)
	}
	b.mu.Unlock()

	if !ok {
		return nil
	}

	session.stop()
	session.wait(2 * time.Second)
	return nil
}

func (s *AudioStreamSession) handleInput(input []byte) {
	if len(input) == 0 || s.stopped.Load() {
		return
	}

	buffer := make([]byte, len(input))
	copy(buffer, input)

	select {
	case s.buffers <- buffer:
	default:
		select {
		case <-s.buffers:
		default:
		}
		select {
		case s.buffers <- buffer:
		default:
		}
	}
}

func (s *AudioStreamSession) configureTransport(cfg Config, transport *AudioStreamTransport) error {
	if s == nil {
		return nil
	}
	if transport == nil {
		s.clearTransport()
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(transport.Transport))
	if mode == "" {
		s.clearTransport()
		return errors.New("audio stream transport type is missing")
	}
	if mode != "websocket" {
		s.clearTransport()
		return fmt.Errorf("audio stream transport %s is not supported", transport.Transport)
	}

	resolved, err := resolveStreamEndpoint(cfg.BaseURL, transport.URL)
	if err != nil {
		s.clearTransport()
		return err
	}

	headers := make(map[string]string, len(transport.Headers))
	for key, value := range transport.Headers {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		headers[trimmedKey] = trimmedValue
	}

	s.transportMu.Lock()
	s.transport = &AudioStreamTransport{
		Transport: mode,
		URL:       transport.URL,
		Protocol:  strings.TrimSpace(transport.Protocol),
		Headers:   headers,
	}
	s.transportURL = resolved
	s.transportMu.Unlock()

	if err := s.establishBinaryTransport(cfg); err != nil {
		return err
	}
	return nil
}

func (s *AudioStreamSession) clearTransport() {
	if s == nil {
		return
	}
	s.transportMu.Lock()
	conn := s.streamConn
	s.streamConn = nil
	s.transport = nil
	s.transportURL = ""
	s.transportMu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "transport cleared")
	}
}

func resolveStreamEndpoint(baseURL, endpoint string) (string, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return "", errors.New("audio stream endpoint is empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}

	if !parsed.IsAbs() {
		base := strings.TrimSpace(baseURL)
		if base == "" {
			return "", errors.New("audio stream base url is missing")
		}
		baseParsed, err := url.Parse(base)
		if err != nil {
			return "", err
		}
		parsed = baseParsed.ResolveReference(parsed)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "https":
		parsed.Scheme = "wss"
	case "wss":
	default:
		return "", fmt.Errorf("unsupported audio stream scheme: %s", parsed.Scheme)
	}

	return parsed.String(), nil
}

func (s *AudioStreamSession) establishBinaryTransport(cfg Config) error {
	if s == nil {
		return errors.New("audio stream session is not initialized")
	}

	s.transportMu.Lock()
	transport := s.transport
	resolved := s.transportURL
	protocolName := ""
	headers := make(map[string]string)
	if transport != nil {
		protocolName = strings.TrimSpace(transport.Protocol)
		for key, value := range transport.Headers {
			headers[key] = value
		}
	}
	s.transportMu.Unlock()

	if transport == nil || !strings.EqualFold(transport.Transport, "websocket") {
		return errors.New("audio stream binary transport unavailable")
	}
	if resolved == "" {
		return errors.New("audio stream endpoint is not configured")
	}

	httpHeaders := http.Header{}
	if ua := strings.TrimSpace(s.bridge.userAgent()); ua != "" {
		httpHeaders.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(cfg.AuthKey); key != "" {
		httpHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}
	for key, value := range headers {
		httpHeaders.Set(key, value)
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	options := &websocket.DialOptions{
		HTTPHeader:      httpHeaders,
		CompressionMode: websocket.CompressionDisabled,
	}
	if protocolName != "" {
		options.Subprotocols = []string{protocolName}
	}

	conn, _, err := websocket.Dial(dialCtx, resolved, options)
	if err != nil {
		return err
	}

	s.transportMu.Lock()
	if s.streamConn != nil {
		_ = s.streamConn.Close(websocket.StatusNormalClosure, "replaced")
	}
	s.streamConn = conn
	s.transportMu.Unlock()
	return nil
}

func (s *AudioStreamSession) trySendBinary(cfg Config, data []byte, sequence uint64, timestamp time.Time) bool {
	if s == nil {
		return false
	}

	s.transportMu.Lock()
	transport := s.transport
	s.transportMu.Unlock()

	if transport == nil || !strings.EqualFold(strings.TrimSpace(transport.Transport), "websocket") {
		return false
	}

	if err := s.sendBinaryFrame(data, sequence, timestamp); err == nil {
		return true
	}

	if !s.reconnectTransport(cfg) {
		return false
	}

	return s.sendBinaryFrame(data, sequence, timestamp) == nil
}

func (s *AudioStreamSession) sendBinaryFrame(data []byte, sequence uint64, timestamp time.Time) error {
	if s == nil {
		return errors.New("audio stream session is not initialized")
	}

	s.transportMu.Lock()
	conn := s.streamConn
	transport := s.transport
	format := s.format
	s.transportMu.Unlock()

	if conn == nil || transport == nil || !strings.EqualFold(strings.TrimSpace(transport.Transport), "websocket") {
		return errors.New("audio stream binary transport unavailable")
	}

	header := audioBinaryHeader{
		SessionID: s.id,
		Sequence:  sequence,
		Timestamp: timestamp.UTC().Format(time.RFC3339Nano),
		Format:    format,
	}

	headerPayload, err := json.Marshal(header)
	if err != nil {
		return err
	}

	frame := make([]byte, len(headerPayload)+1+len(data))
	copy(frame, headerPayload)
	frame[len(headerPayload)] = '\n'
	copy(frame[len(headerPayload)+1:], data)

	sendCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := conn.Write(sendCtx, websocket.MessageBinary, frame); err != nil {
		s.transportMu.Lock()
		if s.streamConn == conn {
			s.streamConn = nil
		}
		s.transportMu.Unlock()
		return err
	}

	return nil
}

func (s *AudioStreamSession) reconnectTransport(cfg Config) bool {
	if s == nil {
		return false
	}
	if err := s.establishBinaryTransport(cfg); err != nil {
		s.logf("audio stream %s binary transport reconnect failed: %v", s.id, err)
		return false
	}
	return true
}

func (s *AudioStreamSession) closeStreamConn(status websocket.StatusCode, reason string) {
	if s == nil {
		return
	}
	s.transportMu.Lock()
	conn := s.streamConn
	s.streamConn = nil
	s.transportMu.Unlock()
	if conn != nil {
		_ = conn.Close(status, reason)
	}
}

func (s *AudioStreamSession) run() {
	defer close(s.done)

	for {
		select {
		case <-s.runCtx.Done():
			return
		case data := <-s.buffers:
			if data == nil || len(data) == 0 {
				continue
			}

			cfg := s.bridge.config()
			sequence := atomic.AddUint64(&s.sequence, 1)
			capturedAt := time.Now().UTC()

			if s.trySendBinary(cfg, data, sequence, capturedAt) {
				continue
			}

			baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
			if baseURL == "" {
				s.logf("audio stream session %s missing base URL", s.id)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if cfg.Client == nil {
				s.logf("audio stream session %s missing http client", s.id)
				time.Sleep(500 * time.Millisecond)
				continue
			}

			endpoint := fmt.Sprintf("%s/api/agents/%s/audio/chunks", baseURL, url.PathEscape(cfg.AgentID))

			chunk := AudioStreamChunk{
				SessionID: s.id,
				Sequence:  sequence,
				Timestamp: capturedAt.Format(time.RFC3339Nano),
				Format:    s.format,
				Data:      data,
			}

			if err := s.sendChunk(cfg, endpoint, chunk); err != nil {
				s.logf("audio stream send error: %v", err)
			}
		}
	}
}

func (s *AudioStreamSession) sendChunk(cfg Config, endpoint string, chunk AudioStreamChunk) error {
	payload, err := msgpack.Marshal(chunk)
	if err != nil {
		return err
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/msgpack")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(s.bridge.userAgent()); ua != "" {
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
		return fmt.Errorf("audio chunk rejected: status %d", resp.StatusCode)
	}
	return nil
}

func (s *AudioStreamSession) stop() {
	if s == nil {
		return
	}

	if s.stopped.Swap(true) {
		return
	}

	s.clearTransport()

	if s.runCancel != nil {
		s.runCancel()
	}

	if s.device != nil {
		_ = s.device.Stop()
		s.device.Uninit()
	}

	if s.ctx != nil {
		_ = s.ctx.Context.Uninit()
		s.ctx.Free()
	}
}

func (s *AudioStreamSession) wait(timeout time.Duration) {
	if s == nil || s.done == nil {
		return
	}
	if timeout <= 0 {
		<-s.done
		return
	}
	select {
	case <-s.done:
	case <-time.After(timeout):
	}
}

func (b *AudioBridge) startPlayback(ctx context.Context, payload AudioControlCommandPayload) error {
	trackID := strings.TrimSpace(payload.TrackID)
	if trackID == "" {
		return errors.New("track identifier is required for playback")
	}

	trackURL := strings.TrimSpace(payload.TrackURL)
	if trackURL == "" {
		return errors.New("track URL is required for playback")
	}

	cfg := b.config()
	if cfg.Client == nil {
		return errors.New("audio control: missing http client")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trackURL, nil)
	if err != nil {
		return err
	}
	if ua := strings.TrimSpace(b.userAgent()); ua != "" {
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download audio track: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	p := &playbackSession{
		bridge:     b,
		id:         trackID,
		trackID:    trackID,
		url:        trackURL,
		volume:     payload.Volume,
		loop:       payload.Loop,
		pcmData:    data,
		format:     malgo.FormatS16,
		channels:   uint32(payload.Channels),
		sampleRate: uint32(payload.SampleRate),
		done:       make(chan struct{}),
	}
	if p.channels <= 0 {
		p.channels = 1
	}
	if p.sampleRate <= 0 {
		p.sampleRate = 48000
	}
	if p.volume <= 0 {
		p.volume = 1.0
	}

	b.mu.Lock()
	if existing, ok := b.playbacks[trackID]; ok {
		existing.stop()
		delete(b.playbacks, trackID)
	}
	b.mu.Unlock()

	allocatedCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = p.format
	deviceConfig.Playback.Channels = p.channels
	deviceConfig.SampleRate = p.sampleRate
	deviceConfig.Alsa.NoMMap = 1
	deviceConfig.Playback.ShareMode = malgo.Shared

	if payload.OutputDeviceID != "" {
		token, err := parseDeviceID(payload.OutputDeviceID)
		if err == nil {
			deviceConfig.Playback.DeviceID = unsafe.Pointer(&token)
		}
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(output []byte, _ []byte, frameCount uint32) {
			if p.paused.Load() || p.stopped.Load() {
				return
			}

			bytesToCopy := uint32(len(output))
			remaining := uint32(len(p.pcmData)) - p.offset

			if bytesToCopy > remaining {
				copy(output[:remaining], p.pcmData[p.offset:])
				if p.loop {
					p.offset = bytesToCopy - remaining
					if p.offset > uint32(len(p.pcmData)) {
						p.offset = 0
					}
					copy(output[remaining:], p.pcmData[:p.offset])
				} else {
					p.offset = uint32(len(p.pcmData))
					p.stopped.Store(true)
				}
			} else {
				copy(output, p.pcmData[p.offset:p.offset+bytesToCopy])
				p.offset += bytesToCopy
			}

			if p.volume != 1.0 {
				samples := *(*[]int16)(unsafe.Pointer(&output))
				for i := range samples {
					samples[i] = int16(float64(samples[i]) * p.volume)
				}
			}
		},
	}

	device, err := malgo.InitDevice(allocatedCtx.Context, deviceConfig, callbacks)
	if err != nil {
		allocatedCtx.Context.Uninit()
		allocatedCtx.Free()
		return err
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		allocatedCtx.Context.Uninit()
		allocatedCtx.Free()
		return err
	}

	p.ctx = allocatedCtx
	p.device = device

	b.mu.Lock()
	b.playbacks[trackID] = p
	b.mu.Unlock()

	return nil
}

func (b *AudioBridge) pausePlayback(trackID string) error {
	b.mu.Lock()
	p, ok := b.playbacks[trackID]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("no playback found for track %s", trackID)
	}
	p.paused.Store(true)
	return nil
}

func (b *AudioBridge) resumePlayback(trackID string) error {
	b.mu.Lock()
	p, ok := b.playbacks[trackID]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("no playback found for track %s", trackID)
	}
	p.paused.Store(false)
	return nil
}

func (b *AudioBridge) stopPlayback(trackID string) error {
	b.mu.Lock()
	p, ok := b.playbacks[trackID]
	if ok {
		delete(b.playbacks, trackID)
	}
	b.mu.Unlock()
	if !ok {
		return nil
	}
	p.stop()
	return nil
}

func captureAudioInventory() (*AudioDeviceInventory, error) {
	attempts := append([][]malgo.Backend{nil}, fallbackAudioBackendAttempts()...)

	var (
		lastErr       error
		lastInventory *AudioDeviceInventory
	)

	for idx, backends := range attempts {
		ctx, err := malgo.InitContext(backends, malgo.ContextConfig{}, nil)
		if err != nil {
			lastErr = err
			continue
		}

		inventory, err := captureInventoryWithContext(ctx)
		if err != nil {
			lastErr = err
			continue
		}

		lastInventory = inventory
		if len(inventory.Inputs) > 0 || len(inventory.Outputs) > 0 {
			return inventory, nil
		}

		if idx == len(attempts)-1 {
			return inventory, nil
		}
	}

	if lastInventory != nil {
		return lastInventory, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to enumerate audio devices: %w", lastErr)
	}

	return nil, errors.New("failed to enumerate audio devices")
}

func captureInventoryWithContext(ctx *malgo.AllocatedContext) (*AudioDeviceInventory, error) {
	if ctx == nil {
		return nil, errors.New("audio context is nil")
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	return enumerateAudioDevices(ctx)
}

func enumerateAudioDevices(ctx *malgo.AllocatedContext) (*AudioDeviceInventory, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	inventory := &AudioDeviceInventory{
		Inputs:     make([]AudioDeviceDescriptor, 0),
		Outputs:    make([]AudioDeviceDescriptor, 0),
		CapturedAt: now,
	}

	var (
		enumerated bool
		lastErr    error
	)

	if playback, err := ctx.Devices(malgo.Playback); err == nil {
		enumerated = true
		for idx, info := range playback {
			label := strings.TrimSpace(info.Name())
			if label == "" {
				label = fmt.Sprintf("Playback %d", idx+1)
			}
			descriptor := AudioDeviceDescriptor{
					ID:                    info.ID.String(),
					DeviceID:              info.ID.String(),
					Label:                 label,
					Kind:                  AudioDirectionOutput,
					GroupID:               "",
					SystemDefault:         info.IsDefault != 0,
					CommunicationsDefault: false,
					LastSeen:              now,
			}
			inventory.Outputs = append(inventory.Outputs, descriptor)
		}
	} else {
		lastErr = fmt.Errorf("failed to enumerate playback devices: %w", err)
	}

	if capture, err := ctx.Devices(malgo.Capture); err == nil {
		enumerated = true
		for idx, info := range capture {
			label := strings.TrimSpace(info.Name())
			if label == "" {
				label = fmt.Sprintf("Microphone %d", idx+1)
			}
			descriptor := AudioDeviceDescriptor{
					ID:                    info.ID.String(),
					DeviceID:              info.ID.String(),
					Label:                 label,
					Kind:                  AudioDirectionInput,
					GroupID:               "",
					SystemDefault:         info.IsDefault != 0,
					CommunicationsDefault: false,
					LastSeen:              now,
			}
			inventory.Inputs = append(inventory.Inputs, descriptor)
		}
	} else {
		lastErr = fmt.Errorf("failed to enumerate capture devices: %w", err)
	}

	if enumerated {
		return inventory, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return inventory, nil
}

func parseDeviceID(value string) (malgo.DeviceID, error) {
	var id malgo.DeviceID
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return id, errors.New("empty device identifier")
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return id, err
	}
	if len(decoded) > len(id) {
		return id, errors.New("device identifier is too long")
	}
	copy(id[:], decoded)
	return id, nil
}

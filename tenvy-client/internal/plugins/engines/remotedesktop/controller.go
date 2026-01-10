package remotedesktopengine

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	errSessionStopped  = errors.New("remote desktop session stopped")
	errSessionReplaced = errors.New("remote desktop session replaced")
	errSessionShutdown = errors.New("remote desktop subsystem shutdown")
)

const (
	defaultFrameRequestTimeout = 10 * time.Second
	minFrameRequestTimeout     = 2 * time.Second
	maxFrameRequestTimeout     = 20 * time.Second
)

const (
	defaultQuicInputPort      = 9543
	defaultQuicInputALPN      = "tenvy.remote-desktop.input.v1"
	defaultQuicConnectTimeout = 5 * time.Second
	defaultQuicRetryInterval  = 2 * time.Second
)

type frameEndpointCache struct {
	base     string
	agentID  string
	endpoint string
}

type transportEndpointCache struct {
	base     string
	agentID  string
	endpoint string
}

func NewRemoteDesktopStreamer(cfg Config) *RemoteDesktopStreamer {
	return &RemoteDesktopStreamer{
		controller: newRemoteDesktopSessionController(cfg),
	}
}

func (s *RemoteDesktopStreamer) Configure(cfg Config) error {
	if s == nil || s.controller == nil {
		return errors.New("remote desktop subsystem not initialized")
	}
	s.controller.updateConfig(cfg)
	return nil
}

func newRemoteDesktopSessionController(cfg Config) *remoteDesktopSessionController {
	controller := &remoteDesktopSessionController{}
	controller.updateConfig(cfg)
	return controller
}

func (s *RemoteDesktopStreamer) UpdateConfig(cfg Config) {
	if s == nil || s.controller == nil {
		return
	}
	s.controller.updateConfig(cfg)
}

func (s *RemoteDesktopStreamer) StartSession(ctx context.Context, payload RemoteDesktopCommandPayload) error {
	if s == nil || s.controller == nil {
		return errors.New("remote desktop subsystem not initialized")
	}
	return s.controller.Start(ctx, payload)
}

func (s *RemoteDesktopStreamer) StopSession(sessionID string) error {
	if s == nil || s.controller == nil {
		return errors.New("remote desktop subsystem not initialized")
	}
	return s.controller.Stop(sessionID)
}

func (s *RemoteDesktopStreamer) UpdateSession(payload RemoteDesktopCommandPayload) error {
	if s == nil || s.controller == nil {
		return errors.New("remote desktop subsystem not initialized")
	}
	return s.controller.Configure(payload)
}

func (s *RemoteDesktopStreamer) HandleInput(ctx context.Context, payload RemoteDesktopCommandPayload) error {
	if s == nil || s.controller == nil {
		return errors.New("remote desktop subsystem not initialized")
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return errors.New("missing session identifier")
	}
	return s.controller.HandleInput(payload)
}

func (s *RemoteDesktopStreamer) DeliverFrame(ctx context.Context, frame RemoteDesktopFramePacket) error {
	if s == nil || s.controller == nil {
		return errors.New("remote desktop subsystem not initialized")
	}
	return s.controller.DeliverFrame(ctx, frame)
}

func (s *RemoteDesktopStreamer) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	payload, err := decodeRemoteDesktopPayload(cmd.Payload)
	if err != nil {
		return CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       err.Error(),
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}

	var actionErr error
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "start":
		actionErr = s.controller.Start(ctx, payload)
	case "stop":
		actionErr = s.controller.Stop(payload.SessionID)
	case "configure":
		actionErr = s.controller.Configure(payload)
	case "input":
		actionErr = s.controller.HandleInput(payload)
	default:
		actionErr = fmt.Errorf("unsupported remote desktop action: %s", payload.Action)
	}

	result := CommandResult{
		CommandID:   cmd.ID,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if actionErr != nil {
		result.Success = false
		result.Error = actionErr.Error()
	} else {
		result.Success = true
		result.Output = fmt.Sprintf("remote desktop %s action processed", payload.Action)
	}
	return result
}

func (s *RemoteDesktopStreamer) Shutdown() {
	s.controller.Shutdown()
}

func (s *RemoteDesktopStreamer) HandleInputPayload(ctx context.Context, payload RemoteDesktopCommandPayload) error {
	if s == nil || s.controller == nil {
		return errors.New("remote desktop subsystem not initialized")
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return errors.New("missing session identifier")
	}
	if strings.TrimSpace(payload.Action) == "" {
		payload.Action = "input"
	}
	return s.controller.HandleInput(payload)
}

func DecodeCommandPayload(raw json.RawMessage) (RemoteDesktopCommandPayload, error) {
	var payload RemoteDesktopCommandPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return RemoteDesktopCommandPayload{}, fmt.Errorf("invalid remote desktop payload: %w", err)
	}
	return payload, nil
}

func decodeRemoteDesktopPayload(raw json.RawMessage) (RemoteDesktopCommandPayload, error) {
	return DecodeCommandPayload(raw)
}

func (c *remoteDesktopSessionController) Start(ctx context.Context, payload RemoteDesktopCommandPayload) error {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return errors.New("missing session identifier")
	}

	for {
		c.mu.Lock()
		if c.session == nil || c.session.ID == sessionID {
			break
		}
		prev := c.stopLocked(errSessionReplaced)
		c.mu.Unlock()
		waitSession(prev)
	}

	if c.session != nil && c.session.ID == sessionID {
		c.applySettingsLocked(c.session, payload.Settings)
		c.mu.Unlock()
		return nil
	}

	settings := defaultRemoteDesktopSettings()
	applySettingsPatch(&settings, payload.Settings)

	monitors := detectRemoteMonitors()
	infos := monitorInfos(monitors)
	if len(infos) == 0 {
		infos = []RemoteDesktopMonitorInfo{{ID: 0, Label: "Primary", Width: 1280, Height: 720}}
		monitors = []remoteMonitor{{
			info:   infos[0],
			bounds: image.Rect(0, 0, infos[0].Width, infos[0].Height),
		}}
	}

	settings.Monitor = clampMonitorIndex(monitors, settings.Monitor)
	monitorInfo := infos[settings.Monitor]
	streamCtx, cancel := context.WithCancelCause(context.Background())
	session := &RemoteDesktopSession{
		ID:              sessionID,
		Settings:        settings,
		ForceKeyFrame:   true,
		monitors:        monitors,
		monitorInfos:    infos,
		monitorsDirty:   true,
		ctx:             streamCtx,
		cancel:          cancel,
		EncoderHardware: "",
	}
	session.ActiveEncoder = normalizeEncoder(session.Settings.Encoder)
	if session.ActiveEncoder == "" {
		session.ActiveEncoder = RemoteEncoderAuto
	}
	session.NegotiatedCodec = session.ActiveEncoder
	session.Transport = RemoteTransportHTTP
	session.wg.Add(1)
	profile, ladder, idx := selectQualityProfile(settings.Quality, monitorInfo)
	session.qualityLadder = ladder
	session.ladderIndex = idx
	c.configureProfileLocked(session, monitorInfo, profile, true)
	c.session = session
	c.mu.Unlock()

	if err := c.initializeTransport(streamCtx, session); err != nil {
		c.logf("remote desktop transport negotiation failed: %v", err)
	}

	go c.stream(streamCtx, session)
	c.logf("remote desktop session %s started", sessionID)
	if session.ActiveEncoder != "" {
		c.logf("remote desktop session %s encoder preference: %s", sessionID, session.ActiveEncoder)
	}
	return nil
}

func (c *remoteDesktopSessionController) Stop(sessionID string) error {
	trimmed := strings.TrimSpace(sessionID)

	c.mu.Lock()
	if c.session == nil {
		c.mu.Unlock()
		return nil
	}
	if trimmed != "" && trimmed != c.session.ID {
		c.mu.Unlock()
		return fmt.Errorf("session %s not active", trimmed)
	}
	stopped := c.stopLocked(errSessionStopped)
	c.mu.Unlock()

	waitSession(stopped)
	if stopped != nil {
		c.logf("remote desktop session %s stopped", stopped.ID)
	}
	return nil
}

func (c *remoteDesktopSessionController) initializeTransport(ctx context.Context, session *RemoteDesktopSession) error {
	if session == nil {
		return errors.New("remote desktop: missing session")
	}

	cfg := c.config()
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = defaultFrameRequestTimeout
	}
	var cancel context.CancelFunc
	negotiationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	codecs := []RemoteDesktopEncoder{RemoteEncoderHEVC, RemoteEncoderAVC, RemoteEncoderJPEG}
	supportsIntra := normalizeStreamMode(session.Settings.Mode) == RemoteStreamModeVideo
	preferredTransport := normalizeTransport(session.Settings.Transport)
	preferWebRTC := preferredTransport != RemoteTransportHTTP

	transports := []RemoteDesktopTransportCapability{{
		Transport: RemoteTransportHTTP,
		Codecs:    append([]RemoteDesktopEncoder(nil), codecs...),
	}}

	var offerHandle *webrtcOfferHandle
	var offerErr error
	if preferWebRTC {
		offerHandle, offerErr = prepareWebRTCOffer(negotiationCtx, cfg.WebRTCICEServers)
		if offerErr == nil && offerHandle != nil {
			features := map[string]bool{}
			if supportsIntra {
				features["intraRefresh"] = true
			}
			features["binaryFrames"] = true
			transports = append([]RemoteDesktopTransportCapability{
				{
					Transport: RemoteTransportWebRTC,
					Codecs:    append([]RemoteDesktopEncoder(nil), codecs...),
					Features:  features,
				},
			}, transports...)
		} else if offerErr != nil {
			c.logf("remote desktop webrtc negotiation unavailable: %v", offerErr)
		}
	}

	request := RemoteDesktopSessionNegotiationRequest{
		SessionID:    session.ID,
		Transports:   transports,
		Codecs:       append([]RemoteDesktopEncoder(nil), codecs...),
		IntraRefresh: supportsIntra,
	}
	if version := strings.TrimSpace(cfg.PluginVersion); version != "" {
		request.PluginVersion = version
	}
	if offerHandle != nil {
		request.WebRTC = &RemoteDesktopWebRTCOffer{
			Offer:       offerHandle.Offer(),
			DataChannel: offerHandle.Label(),
			ICEServers:  append([]RemoteDesktopWebRTCICEServer(nil), cfg.WebRTCICEServers...),
		}
	}

	response, err := c.sendNegotiationRequest(negotiationCtx, request)

	selectedTransport := RemoteTransportHTTP
	selectedCodec := session.NegotiatedCodec
	if selectedCodec == "" {
		selectedCodec = session.ActiveEncoder
	}
	if selectedCodec == "" {
		selectedCodec = RemoteEncoderAuto
	}
	selectedIntra := false
	selectedFeatures := map[string]bool{}
	var sender frameTransport

	if err != nil {
		if offerHandle != nil {
			offerHandle.Close()
		}
		c.assignSessionTransport(session, selectedTransport, nil, selectedCodec, selectedIntra, nil)
		c.stopInputBridge(session)
		return err
	}

	requiredVersion := strings.TrimSpace(response.RequiredPluginVersion)
	if response.Accepted {
		if response.Transport != "" {
			selectedTransport = response.Transport
		}
		if normalized := normalizeEncoder(response.Codec); normalized != "" {
			selectedCodec = normalized
		}
		selectedIntra = response.IntraRefresh
		if len(response.Features) > 0 {
			selectedFeatures = cloneTransportFeatures(response.Features)
		}

		if selectedTransport == RemoteTransportWebRTC {
			if offerHandle != nil && response.WebRTC != nil {
				sender, err = offerHandle.Accept(negotiationCtx, response.WebRTC.Answer)
				if err != nil {
					c.logf("remote desktop webrtc establishment failed: %v", err)
					selectedTransport = RemoteTransportHTTP
					sender = nil
					selectedFeatures = nil
				}
			} else {
				selectedTransport = RemoteTransportHTTP
				selectedFeatures = nil
			}
		}
	} else {
		if offerHandle != nil {
			offerHandle.Close()
		}
		reason := strings.TrimSpace(response.Reason)
		if reason != "" {
			err = errors.New(reason)
		} else if requiredVersion != "" {
			err = fmt.Errorf("remote desktop engine plugin version %s required", requiredVersion)
		} else {
			err = errors.New("remote desktop negotiation rejected")
		}
		c.assignSessionTransport(session, selectedTransport, nil, selectedCodec, selectedIntra, nil)
		c.stopInputBridge(session)
		return err
	}

	if offerHandle != nil && (selectedTransport != RemoteTransportWebRTC || sender == nil) {
		offerHandle.Close()
	}

	if selectedTransport != RemoteTransportWebRTC {
		selectedIntra = false
		selectedFeatures = nil
	}

	if wt, ok := sender.(*webrtcFrameTransport); ok {
		wt.SetBinaryEnabled(selectedFeatures["binaryFrames"])
	}
	c.assignSessionTransport(session, selectedTransport, sender, selectedCodec, selectedIntra, selectedFeatures)
	c.configureInputBridge(session, response.Input)
	if requiredVersion != "" && !strings.EqualFold(requiredVersion, strings.TrimSpace(cfg.PluginVersion)) {
		c.logf("remote desktop engine plugin requires version %s (current %s)", requiredVersion, strings.TrimSpace(cfg.PluginVersion))
	}
	if selectedTransport == RemoteTransportWebRTC && sender == nil {
		if err != nil {
			return err
		}
		return errors.New("remote desktop: webrtc transport unavailable")
	}
	return err
}

func (c *remoteDesktopSessionController) sendNegotiationRequest(ctx context.Context, request RemoteDesktopSessionNegotiationRequest) (RemoteDesktopSessionNegotiationResponse, error) {
	cfg := c.config()
	endpoint, err := c.transportEndpoint(cfg)
	if err != nil {
		return RemoteDesktopSessionNegotiationResponse{}, err
	}
	client := cfg.Client
	if client == nil {
		return RemoteDesktopSessionNegotiationResponse{}, errors.New("remote desktop: missing http client")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if timeout := cfg.RequestTimeout; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	body := acquireJSONBody()
	defer releaseJSONBody(body)

	encoder := json.NewEncoder(body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(request); err != nil {
		return RemoteDesktopSessionNegotiationResponse{}, err
	}
	if body.Len() > 0 {
		raw := body.Bytes()
		if raw[len(raw)-1] == '\n' {
			body.Truncate(body.Len() - 1)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return RemoteDesktopSessionNegotiationResponse{}, err
	}
	req.ContentLength = int64(body.Len())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(c.userAgent()); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if cfg.authHeader != "" {
		req.Header.Set("Authorization", cfg.authHeader)
	}

	resp, err := client.Do(req)
	if err != nil {
		return RemoteDesktopSessionNegotiationResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		drainErr := drainResponseBody(resp.Body)
		if drainErr != nil {
			return RemoteDesktopSessionNegotiationResponse{}, fmt.Errorf("transport negotiation failed: status %d: %w", resp.StatusCode, drainErr)
		}
		return RemoteDesktopSessionNegotiationResponse{}, fmt.Errorf("transport negotiation failed: status %d", resp.StatusCode)
	}

	var response RemoteDesktopSessionNegotiationResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&response); err != nil {
		return RemoteDesktopSessionNegotiationResponse{}, fmt.Errorf("transport negotiation response invalid: %w", err)
	}
	return response, nil
}

func cloneTransportFeatures(features map[string]bool) map[string]bool {
	if len(features) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(features))
	for key, value := range features {
		cloned[key] = value
	}
	return cloned
}

func (c *remoteDesktopSessionController) assignSessionTransport(session *RemoteDesktopSession, transport RemoteDesktopTransport, sender frameTransport, codec RemoteDesktopEncoder, intra bool, features map[string]bool) {
	var toClose frameTransport
	var replaced frameTransport
	var changed bool
	c.mu.Lock()
	if c.session == nil || session == nil || c.session.ID != session.ID {
		toClose = sender
	} else {
		if c.session.transport != nil && c.session.transport != sender {
			replaced = c.session.transport
		}
		if codec != "" {
			c.session.NegotiatedCodec = codec
		}
		c.session.IntraRefresh = intra
		previous := c.session.Transport
		c.session.Transport = transport
		c.session.transport = sender
		c.session.Settings.Transport = transport
		c.session.TransportFeatures = cloneTransportFeatures(features)

		session.Transport = transport
		session.transport = sender
		session.Settings.Transport = transport
		if codec != "" {
			session.NegotiatedCodec = codec
		}
		session.IntraRefresh = intra
		session.TransportFeatures = cloneTransportFeatures(features)

		changed = previous != transport
	}
	c.mu.Unlock()

	if replaced != nil {
		replaced.Close()
	}
	if toClose != nil {
		toClose.Close()
	}

	if changed {
		c.logf("remote desktop session %s using %s transport", session.ID, transport)
	}
}

func (c *remoteDesktopSessionController) closeSessionTransport(session *RemoteDesktopSession) {
	var toClose frameTransport
	c.mu.Lock()
	if session != nil && session.transport != nil {
		toClose = session.transport
		session.transport = nil
	}
	if c.session != nil && session != nil && c.session.ID == session.ID {
		if c.session.transport != nil && c.session.transport != toClose {
			toClose = c.session.transport
		}
		c.session.transport = nil
	}
	c.mu.Unlock()

	if toClose != nil {
		toClose.Close()
	}
}
func (c *remoteDesktopSessionController) Configure(payload RemoteDesktopCommandPayload) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session == nil {
		return errors.New("remote desktop session not active")
	}
	if strings.TrimSpace(payload.SessionID) != "" && payload.SessionID != c.session.ID {
		return fmt.Errorf("session %s not active", payload.SessionID)
	}

	c.applySettingsLocked(c.session, payload.Settings)
	return nil
}

func (c *remoteDesktopSessionController) HandleInput(payload RemoteDesktopCommandPayload) error {
	if len(payload.Events) == 0 {
		return nil
	}

	sessionID := strings.TrimSpace(payload.SessionID)

	c.mu.Lock()
	if c.session == nil {
		c.mu.Unlock()
		return errors.New("remote desktop session not active")
	}
	if sessionID != "" && sessionID != c.session.ID {
		c.mu.Unlock()
		return fmt.Errorf("session %s not active", sessionID)
	}

	settings := c.session.Settings
	monitors := append([]remoteMonitor(nil), c.session.monitors...)
	c.mu.Unlock()

	filtered := make([]RemoteDesktopInputEvent, 0, len(payload.Events))
	for _, event := range payload.Events {
		switch event.Type {
		case RemoteInputMouseMove, RemoteInputMouseButton, RemoteInputMouseScroll:
			if !settings.Mouse {
				continue
			}
		case RemoteInputKey:
			if !settings.Keyboard {
				continue
			}
		default:
			continue
		}
		filtered = append(filtered, event)
	}

	if len(filtered) == 0 {
		return nil
	}

	return processRemoteInput(monitors, settings, filtered)
}

func (c *remoteDesktopSessionController) DeliverFrame(ctx context.Context, frame RemoteDesktopFramePacket) error {
	sessionID := strings.TrimSpace(frame.SessionID)
	if sessionID == "" {
		return errors.New("missing session identifier")
	}

	c.mu.Lock()
	if c.session == nil || c.session.ID != sessionID {
		c.mu.Unlock()
		return fmt.Errorf("session %s not active", sessionID)
	}
	session := c.session
	session.Sequence++
	frame.Sequence = session.Sequence
	frame.SessionID = session.ID
	c.mu.Unlock()

	if frame.Timestamp == "" {
		frame.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	return c.sendFrame(ctx, session, frame)
}

func (c *remoteDesktopSessionController) Shutdown() {
	c.mu.Lock()
	stopped := c.stopLocked(errSessionShutdown)
	c.mu.Unlock()
	waitSession(stopped)
}

func (c *remoteDesktopSessionController) stopLocked(cause error) *RemoteDesktopSession {
	if c.session == nil {
		return nil
	}
	session := c.session
	if session.inputBridge != nil {
		session.inputBridge.Close()
		session.inputBridge = nil
	}
	if session.cancel != nil {
		if cause == nil {
			cause = errSessionStopped
		}
		session.cancel(cause)
	}
	c.session = nil
	return session
}

func waitSession(session *RemoteDesktopSession) {
	if session == nil {
		return
	}
	session.wg.Wait()
}

func (c *remoteDesktopSessionController) logf(format string, args ...interface{}) {
	cfg := c.config()
	if cfg.Logger == nil {
		return
	}
	cfg.Logger.Printf(format, args...)
}

func (c *remoteDesktopSessionController) updateActiveEncoder(session *RemoteDesktopSession, encoder RemoteDesktopEncoder) {
	normalized := normalizeEncoder(encoder)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil || session == nil || c.session.ID != session.ID {
		return
	}
	if normalized == "" {
		normalized = RemoteEncoderAuto
	}
	if c.session.ActiveEncoder == normalized {
		return
	}
	c.session.ActiveEncoder = normalized
	if normalized != RemoteEncoderAuto {
		c.logf("remote desktop session %s using %s encoder", session.ID, normalized)
	} else {
		c.logf("remote desktop session %s using automatic encoder selection", session.ID)
	}
}

func (c *remoteDesktopSessionController) userAgent() string {
	ua := strings.TrimSpace(c.config().UserAgent)
	if ua != "" {
		return ua
	}
	return "tenvy-client"
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (c *remoteDesktopSessionController) applySettingsLocked(session *RemoteDesktopSession, patch *RemoteDesktopSettingsPatch) {
	if session == nil || patch == nil {
		return
	}

	prevMonitor := session.Settings.Monitor
	prevMode := session.Settings.Mode
	prevQuality := session.Settings.Quality
	qualityChanged := false

	if patch.Quality != nil {
		nextQuality := normalizeQuality(*patch.Quality)
		if nextQuality != session.Settings.Quality {
			qualityChanged = true
		}
		session.Settings.Quality = nextQuality
		session.AdaptiveScale = 1
		session.LastAdaptation = time.Time{}
		session.ClipQuality = 0
	}
	if patch.Mode != nil {
		session.Settings.Mode = normalizeStreamMode(*patch.Mode)
		if session.Settings.Mode != prevMode {
			session.ForceKeyFrame = true
			releaseFrameBuffer(session.LastFrame)
			session.LastFrame = nil
			session.ClipQuality = 0
		}
	}
	if patch.Monitor != nil {
		session.Settings.Monitor = *patch.Monitor
	}
	if patch.Mouse != nil {
		session.Settings.Mouse = *patch.Mouse
	}
	if patch.Keyboard != nil {
		session.Settings.Keyboard = *patch.Keyboard
	}
	if patch.Encoder != nil {
		nextEncoder := normalizeEncoder(*patch.Encoder)
		if nextEncoder != session.Settings.Encoder {
			session.Settings.Encoder = nextEncoder
			if nextEncoder == RemoteEncoderAuto {
				session.ActiveEncoder = RemoteEncoderAuto
			} else {
				session.ActiveEncoder = nextEncoder
			}
			session.ForceKeyFrame = true
			c.logf("remote desktop encoder preference set to %s", nextEncoder)
		}
	}
	if patch.Transport != nil {
		session.Settings.Transport = normalizeTransport(*patch.Transport)
	}
	if patch.Hardware != nil {
		session.Settings.Hardware = normalizeHardware(*patch.Hardware)
	}
	if patch.TargetBitrateKbps != nil {
		target := maxInt(0, *patch.TargetBitrateKbps)
		session.Settings.TargetBitrateKbps = target
		session.TargetBitrateKbps = target
	}

	if len(session.monitors) == 0 || len(session.monitorInfos) == 0 {
		c.refreshMonitorsLocked(session, false)
	}

	session.Settings.Monitor = clampMonitorIndex(session.monitors, session.Settings.Monitor)
	monitorInfo := session.monitorInfos[session.Settings.Monitor]

	if patch.Quality != nil && session.Settings.Quality != prevQuality {
		qualityChanged = true
	}
	profile, ladder, idx := selectQualityProfile(session.Settings.Quality, monitorInfo)
	session.qualityLadder = ladder
	session.ladderIndex = idx
	forceKey := session.Settings.Monitor != prevMonitor || qualityChanged
	c.configureProfileLocked(session, monitorInfo, profile, forceKey)
}

func (c *remoteDesktopSessionController) configureProfileLocked(
	session *RemoteDesktopSession,
	monitor RemoteDesktopMonitorInfo,
	profile remoteQualityProfile,
	forceKey bool,
) {
	if session == nil {
		return
	}

	if session.Settings.Mode == "" {
		session.Settings.Mode = RemoteStreamModeVideo
	}

	width := profile.width
	height := profile.height
	if width <= 0 {
		width = monitor.Width
	}
	if height <= 0 {
		height = monitor.Height
	}
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}
	width = alignEvenDown(width)
	height = alignEvenDown(height)
	if width <= 0 {
		width = 2
	}
	if height <= 0 {
		height = 2
	}
	baseWidth := maxInt(1, width)
	baseHeight := maxInt(1, height)
	session.BaseWidth = baseWidth
	session.BaseHeight = baseHeight

	nativeWidth := alignEvenDown(monitor.Width)
	if nativeWidth <= 0 {
		nativeWidth = baseWidth
	}
	nativeHeight := alignEvenDown(monitor.Height)
	if nativeHeight <= 0 {
		nativeHeight = baseHeight
	}
	session.NativeWidth = nativeWidth
	session.NativeHeight = nativeHeight

	baseTile := profile.tile
	if baseTile <= 0 {
		baseTile = 40
	}
	session.BaseTile = baseTile
	session.MinTile = maxInt(24, baseTile-16)
	session.MaxTile = minInt(120, baseTile+32)
	session.TileSize = clampInt(baseTile, session.MinTile, session.MaxTile)

	session.MinClipQuality = minClipQuality
	session.MaxClipQuality = maxClipQuality
	baseClipQuality := profile.clipQuality
	if baseClipQuality <= 0 {
		baseClipQuality = clipQualityBaseline(session.Settings.Quality)
	}
	if baseClipQuality <= 0 {
		baseClipQuality = defaultClipQuality
	}
	baseClipQuality = clampInt(baseClipQuality, session.MinClipQuality, session.MaxClipQuality)
	session.BaseClipQuality = baseClipQuality
	if session.Settings.Mode != RemoteStreamModeVideo {
		session.ClipQuality = baseClipQuality
	} else {
		if session.ClipQuality == 0 || forceKey {
			session.ClipQuality = baseClipQuality
		} else {
			session.ClipQuality = clampInt(session.ClipQuality, session.MinClipQuality, session.MaxClipQuality)
		}
	}

	baseInterval := profile.interval
	if baseInterval <= 0 {
		baseInterval = 100 * time.Millisecond
	}
	session.BaseInterval = baseInterval
	session.MinInterval = maxDuration(50*time.Millisecond, baseInterval/2)
	session.MaxInterval = minDuration(400*time.Millisecond, baseInterval*2)
	session.FrameInterval = clampDuration(baseInterval, session.MinInterval, session.MaxInterval)

	session.TargetBitrateKbps = maxInt(0, profile.bitrate)

	resolutionChanged := false

	if session.Settings.Quality == RemoteQualityAuto {
		if session.AdaptiveScale <= 0 {
			session.AdaptiveScale = 1
		}
		session.MinScale = 0.5
		maxScale := float64(session.NativeWidth) / float64(session.BaseWidth)
		if maxScale < 1 {
			maxScale = 1
		}
		session.MaxScale = math.Min(1.3, maxScale)
		if session.MaxScale < session.MinScale {
			session.MaxScale = session.MinScale
		}
		session.AdaptiveScale = clampFloat(session.AdaptiveScale, session.MinScale, session.MaxScale)
		if c.applyAdaptiveScaleLocked(session, forceKey) {
			resolutionChanged = true
		}
	} else {
		session.MinScale = 1
		session.MaxScale = 1
		session.AdaptiveScale = 1
		if session.Width != session.BaseWidth || session.Height != session.BaseHeight {
			session.Width = session.BaseWidth
			session.Height = session.BaseHeight
			resolutionChanged = true
		}
	}

	if session.Width == 0 || session.Height == 0 {
		session.Width = session.BaseWidth
		session.Height = session.BaseHeight
	}

	if session.Settings.Quality == RemoteQualityAuto {
		if session.TileSize == 0 {
			session.TileSize = clampInt(session.BaseTile, session.MinTile, session.MaxTile)
		} else {
			session.TileSize = clampInt(session.TileSize, session.MinTile, session.MaxTile)
		}
	} else {
		session.TileSize = clampInt(session.BaseTile, session.MinTile, session.MaxTile)
	}

	session.FrameInterval = clampDuration(session.FrameInterval, session.MinInterval, session.MaxInterval)

	if resolutionChanged {
		forceKey = true
	}

	if forceKey {
		session.LastFrame = nil
		session.ForceKeyFrame = true
		session.LastAdaptation = time.Time{}
		session.bandwidthEMA = 0
		session.fpsEMA = 0
		session.processingEMA = 0
		session.frameDropEMA = 0
	}
}

func (c *remoteDesktopSessionController) applyAdaptiveScaleLocked(session *RemoteDesktopSession, markKeyFrame bool) bool {
	if session == nil || session.Settings.Quality != RemoteQualityAuto {
		return false
	}

	scale := clampFloat(session.AdaptiveScale, session.MinScale, session.MaxScale)
	if scale <= 0 {
		scale = 1
	}

	lowerWidth := int(math.Round(float64(session.BaseWidth) * session.MinScale))
	if lowerWidth <= 0 {
		lowerWidth = session.BaseWidth
	}
	upperWidth := session.NativeWidth
	if upperWidth <= 0 {
		upperWidth = session.BaseWidth
	}
	targetWidth := int(math.Round(float64(session.BaseWidth) * scale))
	width := clampInt(targetWidth, lowerWidth, upperWidth)
	minWidthEven := maxInt(2, alignEvenDown(lowerWidth))
	maxWidthEven := maxInt(minWidthEven, alignEvenDown(upperWidth))
	width = alignEvenDown(width)
	if width < minWidthEven {
		width = minWidthEven
	}
	if maxWidthEven > 0 && width > maxWidthEven {
		width = maxWidthEven
	}
	if width <= 0 {
		width = maxInt(2, alignEvenDown(session.BaseWidth))
	}

	lowerHeight := int(math.Round(float64(session.BaseHeight) * session.MinScale))
	if lowerHeight <= 0 {
		lowerHeight = session.BaseHeight
	}
	upperHeight := session.NativeHeight
	if upperHeight <= 0 {
		upperHeight = session.BaseHeight
	}
	targetHeight := int(math.Round(float64(session.BaseHeight) * scale))
	height := clampInt(targetHeight, lowerHeight, upperHeight)
	minHeightEven := maxInt(2, alignEvenDown(lowerHeight))
	maxHeightEven := maxInt(minHeightEven, alignEvenDown(upperHeight))
	height = alignEvenDown(height)
	if height < minHeightEven {
		height = minHeightEven
	}
	if maxHeightEven > 0 && height > maxHeightEven {
		height = maxHeightEven
	}
	if height <= 0 {
		height = maxInt(2, alignEvenDown(session.BaseHeight))
	}

	if width == session.Width && height == session.Height {
		return false
	}

	session.Width = width
	session.Height = height
	session.LastFrame = nil
	if markKeyFrame {
		session.ForceKeyFrame = true
	}
	return true
}

func (c *remoteDesktopSessionController) maybeAdaptQualityLocked(
	session *RemoteDesktopSession,
	metrics *RemoteDesktopFrameMetrics,
	processing, frameDuration time.Duration,
	bytesSent int,
) {
	if session == nil || session.Settings.Quality != RemoteQualityAuto {
		return
	}
	if len(session.qualityLadder) == 0 {
		return
	}

	now := time.Now()
	if !session.LastAdaptation.IsZero() && now.Sub(session.LastAdaptation) < 400*time.Millisecond {
		return
	}

	var fps float64
	var bandwidth float64
	if metrics != nil {
		fps = metrics.FPS
		bandwidth = metrics.BandwidthKbps
	}
	if fps <= 0 && session.FrameInterval > 0 {
		fps = 1.0 / session.FrameInterval.Seconds()
	}
	if bandwidth <= 0 {
		if frameDuration > 0 {
			bandwidth = float64(bytesSent*8) / 1024 / frameDuration.Seconds()
		} else if session.FrameInterval > 0 {
			bandwidth = float64(bytesSent*8) / 1024 / session.FrameInterval.Seconds()
		}
	}

	const emaAlpha = 0.35
	if fps > 0 {
		session.fpsEMA = updateEMA(session.fpsEMA, fps, emaAlpha)
	}
	if bandwidth > 0 {
		session.bandwidthEMA = updateEMA(session.bandwidthEMA, bandwidth, emaAlpha)
	}
	processingMs := processing.Seconds() * 1000
	if processingMs > 0 {
		session.processingEMA = updateEMA(session.processingEMA, processingMs, emaAlpha)
	}

	ladderIndex := clampInt(session.ladderIndex, 0, len(session.qualityLadder)-1)
	currentProfile := session.qualityLadder[ladderIndex]
	minLadderBitrate := 0
	maxLadderBitrate := 0
	if len(session.qualityLadder) > 0 {
		minLadderBitrate = session.qualityLadder[len(session.qualityLadder)-1].bitrate
		maxLadderBitrate = session.qualityLadder[0].bitrate
	}

	processingBudget := float64(session.FrameInterval.Milliseconds())
	if processingBudget <= 0 {
		processingBudget = float64(currentProfile.interval.Milliseconds())
	}
	if processingBudget <= 0 {
		processingBudget = 100
	}

	fpsSample := session.fpsEMA
	if fpsSample <= 0 {
		fpsSample = fps
	}
	bandwidthSample := session.bandwidthEMA
	if bandwidthSample <= 0 {
		bandwidthSample = bandwidth
	}
	processingSample := session.processingEMA
	if processingSample <= 0 {
		processingSample = processingMs
	}

	dropRate := clampFloat(session.frameDropEMA, 0, 1)

	degrade := false
	improve := false

	if fpsSample > 0 && fpsSample < 12 {
		degrade = true
	}
	if bandwidthSample > 0 && currentProfile.bitrate > 0 && bandwidthSample > float64(currentProfile.bitrate)*1.15 {
		degrade = true
	}
	if processingSample > 0 && processingBudget > 0 && processingSample > processingBudget*0.85 {
		degrade = true
	}
	if session.FrameInterval > 0 && frameDuration > session.FrameInterval+session.FrameInterval/2 {
		degrade = true
	}
	if dropRate > 0.12 {
		degrade = true
	}

	if !degrade && dropRate < 0.08 && session.ladderIndex > 0 {
		prevProfile := session.qualityLadder[session.ladderIndex-1]
		targetBandwidth := float64(prevProfile.bitrate)
		if targetBandwidth <= 0 {
			targetBandwidth = float64(currentProfile.bitrate)
		}
		if fpsSample >= 22 && processingSample < processingBudget*0.65 {
			if (targetBandwidth <= 0 || bandwidthSample <= 0 || bandwidthSample < targetBandwidth*0.78) && dropRate < 0.04 {
				improve = true
			}
		}
	}

	if dropRate > 0.08 {
		improve = false
	}

	if degrade {
		if session.ClipQuality > session.MinClipQuality {
			nextQuality := session.ClipQuality - clipQualityStepDown
			if nextQuality < session.MinClipQuality {
				nextQuality = session.MinClipQuality
			}
			nextQuality = clampInt(nextQuality, session.MinClipQuality, session.MaxClipQuality)
			if nextQuality < session.ClipQuality {
				session.ClipQuality = nextQuality
				session.LastAdaptation = now
				return
			}
		}
		if session.Settings.Mode == RemoteStreamModeImages && session.TileSize < session.MaxTile {
			nextTile := clampInt(session.TileSize+8, session.MinTile, session.MaxTile)
			if nextTile > session.TileSize {
				session.TileSize = nextTile
				session.LastAdaptation = now
				return
			}
		}
		if session.FrameInterval < session.MaxInterval {
			nextInterval := time.Duration(float64(session.FrameInterval) * 1.25)
			if nextInterval <= session.FrameInterval {
				nextInterval = session.FrameInterval + 15*time.Millisecond
			}
			nextInterval = clampDuration(nextInterval, session.MinInterval, session.MaxInterval)
			if nextInterval > session.FrameInterval {
				session.FrameInterval = nextInterval
				session.LastAdaptation = now
				return
			}
		}
		nextScale := clampFloat(session.AdaptiveScale*0.85, session.MinScale, session.MaxScale)
		if nextScale < session.AdaptiveScale-0.01 {
			session.AdaptiveScale = nextScale
			if c.applyAdaptiveScaleLocked(session, true) {
				session.LastAdaptation = now
				return
			}
		}
		if session.TargetBitrateKbps > 0 {
			lowerBound := minLadderBitrate
			if lowerBound <= 0 {
				lowerBound = int(float64(session.TargetBitrateKbps) * 0.5)
			}
			nextBitrate := int(math.Round(float64(session.TargetBitrateKbps) * 0.7))
			if lowerBound > 0 && nextBitrate < lowerBound {
				nextBitrate = lowerBound
			}
			if nextBitrate < session.TargetBitrateKbps {
				session.TargetBitrateKbps = nextBitrate
				session.LastAdaptation = now
				return
			}
		}
		if session.ladderIndex < len(session.qualityLadder)-1 {
			session.ladderIndex++
			session.AdaptiveScale = 1
			monitorIndex := clampMonitorIndex(session.monitors, session.Settings.Monitor)
			monitor := monitorInfoAt(session, monitorIndex)
			profile := session.qualityLadder[session.ladderIndex]
			c.configureProfileLocked(session, monitor, profile, true)
			session.LastAdaptation = now
			return
		}
	}

	if improve {
		if session.FrameInterval > session.MinInterval {
			target := session.BaseInterval
			if target <= 0 {
				target = session.MinInterval
			}
			nextInterval := time.Duration(float64(session.FrameInterval) * 0.85)
			if nextInterval < target {
				nextInterval = target
			}
			nextInterval = clampDuration(nextInterval, session.MinInterval, session.MaxInterval)
			if nextInterval < session.FrameInterval {
				session.FrameInterval = nextInterval
				session.LastAdaptation = now
				return
			}
		}
		if session.Settings.Mode == RemoteStreamModeImages && session.TileSize > session.MinTile {
			baseline := clampInt(session.BaseTile, session.MinTile, session.MaxTile)
			nextTile := clampInt(session.TileSize-6, session.MinTile, session.MaxTile)
			if nextTile < baseline {
				nextTile = baseline
			}
			if nextTile < session.TileSize {
				session.TileSize = nextTile
				session.LastAdaptation = now
				return
			}
		}
		if session.ClipQuality < session.MaxClipQuality {
			targetQuality := session.BaseClipQuality
			if targetQuality <= 0 {
				targetQuality = session.MaxClipQuality
			}
			nextQuality := session.ClipQuality + clipQualityStepUp
			if nextQuality > targetQuality {
				nextQuality = targetQuality
			}
			nextQuality = clampInt(nextQuality, session.MinClipQuality, session.MaxClipQuality)
			if nextQuality > session.ClipQuality {
				session.ClipQuality = nextQuality
				session.LastAdaptation = now
				return
			}
		}
		if session.TargetBitrateKbps > 0 {
			upperBound := maxLadderBitrate
			if upperBound <= 0 {
				upperBound = session.TargetBitrateKbps
			}
			step := maxInt(120, session.TargetBitrateKbps/10)
			nextBitrate := session.TargetBitrateKbps + step
			if upperBound > 0 && nextBitrate > upperBound {
				nextBitrate = upperBound
			}
			if nextBitrate > session.TargetBitrateKbps {
				session.TargetBitrateKbps = nextBitrate
				session.LastAdaptation = now
				return
			}
		}
		nextScale := clampFloat(session.AdaptiveScale+0.08, session.MinScale, session.MaxScale)
		if nextScale > session.AdaptiveScale+0.01 {
			session.AdaptiveScale = nextScale
			if c.applyAdaptiveScaleLocked(session, true) {
				session.LastAdaptation = now
				return
			}
		}
		if session.ladderIndex > 0 {
			session.ladderIndex--
			session.AdaptiveScale = 1
			monitorIndex := clampMonitorIndex(session.monitors, session.Settings.Monitor)
			monitor := monitorInfoAt(session, monitorIndex)
			profile := session.qualityLadder[session.ladderIndex]
			c.configureProfileLocked(session, monitor, profile, true)
			session.LastAdaptation = now
			return
		}
	}
}

func (c *remoteDesktopSessionController) updateConfig(cfg Config) {
	sanitized := sanitizeConfig(cfg)
	c.cfg.Store(sanitized)
	c.endpointCache.Store(frameEndpointCache{})
	c.transportCache.Store(transportEndpointCache{})
}

func (c *remoteDesktopSessionController) config() Config {
	if value := c.cfg.Load(); value != nil {
		return value.(Config)
	}
	return Config{}
}

func sanitizeConfig(cfg Config) Config {
	cfg.AgentID = strings.TrimSpace(cfg.AgentID)
	cfg.BaseURL = normalizeBaseURL(strings.TrimSpace(cfg.BaseURL))
	cfg.AuthKey = strings.TrimSpace(cfg.AuthKey)
	cfg.PluginVersion = strings.TrimSpace(cfg.PluginVersion)
	cfg.Client = secureHTTPClient(cfg.Client)
	cfg.RequestTimeout = normalizeRequestTimeout(cfg.RequestTimeout)
	cfg.WebRTCICEServers = normalizeICEServers(cfg.WebRTCICEServers)
	cfg.authHeader = buildAuthHeader(cfg.AuthKey)
	cfg.quicInput = sanitizeQUICInputConfig(cfg)
	return cfg
}

func (c *remoteDesktopSessionController) configureInputBridge(session *RemoteDesktopSession, hints *RemoteDesktopInputNegotiation) {
	if session == nil {
		return
	}
	cfg := c.config()
	if hints == nil || hints.QUIC == nil || !hints.QUIC.Enabled {
		c.stopInputBridge(session)
		return
	}

	base := cfg.quicInput
	if !base.enabled {
		c.stopInputBridge(session)
		return
	}

	sanitized := base
	hint := hints.QUIC
	if hint.Port > 0 {
		host, _, err := net.SplitHostPort(sanitized.address)
		if err == nil && host != "" {
			sanitized.address = net.JoinHostPort(host, strconv.Itoa(hint.Port))
		}
	}
	if alpn := strings.TrimSpace(hint.ALPN); alpn != "" {
		sanitized.alpn = alpn
	}

	bridge := newQuicInputBridge(sanitized, cfg.AgentID, cfg.Logger, func(events []RemoteDesktopInputEvent) error {
		return c.handleInputFromBridge(session, events)
	})
	if bridge == nil {
		c.stopInputBridge(session)
		return
	}

	c.stopInputBridge(session)
	session.inputBridge = bridge
	bridge.Start(session.ctx, session.ID)
}

func (c *remoteDesktopSessionController) stopInputBridge(session *RemoteDesktopSession) {
	if session == nil || session.inputBridge == nil {
		return
	}
	session.inputBridge.Close()
	session.inputBridge = nil
}

func (c *remoteDesktopSessionController) handleInputFromBridge(session *RemoteDesktopSession, events []RemoteDesktopInputEvent) error {
	if session == nil {
		return errors.New("remote desktop session not active")
	}
	if len(events) == 0 {
		return nil
	}
	payload := RemoteDesktopCommandPayload{
		Action:    "input",
		SessionID: session.ID,
		Events:    append([]RemoteDesktopInputEvent(nil), events...),
	}
	return c.HandleInput(payload)
}

func normalizeICEServers(servers []RemoteDesktopWebRTCICEServer) []RemoteDesktopWebRTCICEServer {
	if len(servers) == 0 {
		return nil
	}

	normalized := make([]RemoteDesktopWebRTCICEServer, 0, len(servers))
	for _, server := range servers {
		if len(server.URLs) == 0 {
			continue
		}

		urls := make([]string, 0, len(server.URLs))
		for _, raw := range server.URLs {
			trimmed := strings.TrimSpace(raw)
			if trimmed != "" {
				urls = append(urls, trimmed)
			}
		}
		if len(urls) == 0 {
			continue
		}

		normalized = append(normalized, RemoteDesktopWebRTCICEServer{
			URLs:           urls,
			Username:       strings.TrimSpace(server.Username),
			Credential:     strings.TrimSpace(server.Credential),
			CredentialType: strings.ToLower(strings.TrimSpace(server.CredentialType)),
		})
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeRequestTimeout(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultFrameRequestTimeout
	}
	return clampDuration(value, minFrameRequestTimeout, maxFrameRequestTimeout)
}

func normalizeBaseURL(raw string) string {
	if raw == "" {
		return raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	parsed.Fragment = ""
	if parsed.User != nil {
		// Credentials should never be embedded in the base URL for security reasons.
		parsed.User = nil
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String()
}

func sanitizeQUICInputConfig(cfg Config) sanitizedQUICInput {
	raw := cfg.QUICInput
	result := sanitizedQUICInput{}
	if raw.Disabled {
		return result
	}

	address, serverName := deriveQuicAddress(strings.TrimSpace(raw.URL), cfg.BaseURL)
	if address == "" || serverName == "" {
		return result
	}

	alpn := strings.TrimSpace(raw.ALPN)
	if alpn == "" {
		alpn = defaultQuicInputALPN
	}

	result.enabled = true
	result.address = address
	result.serverName = serverName
	result.alpn = alpn
	result.token = strings.TrimSpace(raw.Token)
	if raw.ConnectTimeout > 0 {
		result.connectTimeout = raw.ConnectTimeout
	} else {
		result.connectTimeout = defaultQuicConnectTimeout
	}
	if raw.RetryInterval > 0 {
		result.retryInterval = raw.RetryInterval
	} else {
		result.retryInterval = defaultQuicRetryInterval
	}

	if raw.InsecureSkipVerify {
		if cfg.Logger != nil {
			cfg.Logger.Printf("remote desktop: insecureSkipVerify is no longer supported; configure trust anchors instead")
		}
	}

	if pool := loadAdditionalRootCAs(raw.RootCAFiles, raw.RootCAPEMs, cfg.Logger); pool != nil {
		result.rootCAs = pool
	}
	if pins := parsePinnedSPKIHashes(raw.PinnedSPKIHashes, cfg.Logger); len(pins) > 0 {
		result.spkiPins = pins
	}

	return result
}

func deriveQuicAddress(override string, base string) (string, string) {
	trimmed := strings.TrimSpace(override)
	if trimmed != "" {
		if !strings.Contains(trimmed, "://") {
			trimmed = "quic://" + trimmed
		}
		if parsed, err := url.Parse(trimmed); err == nil {
			host := strings.TrimSpace(parsed.Hostname())
			if host != "" {
				port := parsed.Port()
				if port == "" {
					port = strconv.Itoa(defaultQuicInputPort)
				}
				return net.JoinHostPort(host, port), host
			}
		}
		return "", ""
	}

	base = strings.TrimSpace(base)
	if base == "" {
		return "", ""
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", ""
	}
	port := parsed.Port()
	if port == "" {
		port = strconv.Itoa(defaultQuicInputPort)
	}
	return net.JoinHostPort(host, port), host
}

func loadAdditionalRootCAs(files, pems []string, logger Logger) *x509.CertPool {
	if len(files) == 0 && len(pems) == 0 {
		return nil
	}

	var pool *x509.CertPool

	appendFromPEM := func(pemData []byte, source string) {
		if len(bytes.TrimSpace(pemData)) == 0 {
			return
		}
		if pool == nil {
			pool = loadSystemCertPool(logger)
			if pool == nil {
				pool = x509.NewCertPool()
			}
		}
		if !pool.AppendCertsFromPEM(pemData) && logger != nil {
			logger.Printf("remote desktop: unable to parse certificate from %s", source)
		}
	}

	for _, path := range files {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		pemData, err := os.ReadFile(trimmed)
		if err != nil {
			if logger != nil {
				logger.Printf("remote desktop: failed to read root CA file %q: %v", trimmed, err)
			}
			continue
		}
		appendFromPEM(pemData, fmt.Sprintf("file %q", trimmed))
	}

	for idx, blob := range pems {
		trimmed := strings.TrimSpace(blob)
		if trimmed == "" {
			continue
		}
		appendFromPEM([]byte(trimmed), fmt.Sprintf("inline root CA #%d", idx+1))
	}

	return pool
}

func loadSystemCertPool(logger Logger) *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil && logger != nil {
		logger.Printf("remote desktop: failed to load system certificate pool: %v", err)
	}
	return pool
}

func parsePinnedSPKIHashes(values []string, logger Logger) [][]byte {
	if len(values) == 0 {
		return nil
	}

	pins := make([][]byte, 0, len(values))
	seen := make(map[string]struct{})

	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		normalized := strings.Map(func(r rune) rune {
			switch r {
			case ':', ' ', '\t', '\n', '\r':
				return -1
			default:
				return r
			}
		}, trimmed)

		var decoded []byte
		if len(normalized) == spkiHashLength*2 {
			if value, err := hex.DecodeString(normalized); err == nil {
				decoded = value
			}
		}
		if decoded == nil {
			if value, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
				decoded = value
			} else if value, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil {
				decoded = value
			}
		}

		if len(decoded) != spkiHashLength {
			if logger != nil {
				logger.Printf("remote desktop: ignoring invalid SPKI pin %q", raw)
			}
			continue
		}

		key := string(decoded)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		pins = append(pins, decoded)
	}

	return pins
}

func (c *remoteDesktopSessionController) frameEndpoint(cfg Config) (string, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return "", errors.New("remote desktop: missing base URL")
	}

	agentID := strings.TrimSpace(cfg.AgentID)
	if agentID == "" {
		return "", errors.New("remote desktop: missing agent identifier")
	}

	if value := c.endpointCache.Load(); value != nil {
		if cached, ok := value.(frameEndpointCache); ok {
			if cached.base == base && cached.agentID == agentID && cached.endpoint != "" {
				return cached.endpoint, nil
			}
		}
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("remote desktop: invalid base URL: %w", err)
	}

	if err := enforceEndpointSecurity(parsed); err != nil {
		return "", err
	}

	pathRef := &url.URL{Path: fmt.Sprintf("/api/agents/%s/remote-desktop/frames", url.PathEscape(agentID))}
	endpoint := parsed.ResolveReference(pathRef).String()
	c.endpointCache.Store(frameEndpointCache{base: base, agentID: agentID, endpoint: endpoint})
	return endpoint, nil
}

func (c *remoteDesktopSessionController) transportEndpoint(cfg Config) (string, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return "", errors.New("remote desktop: missing base URL")
	}

	agentID := strings.TrimSpace(cfg.AgentID)
	if agentID == "" {
		return "", errors.New("remote desktop: missing agent identifier")
	}

	if value := c.transportCache.Load(); value != nil {
		if cached, ok := value.(transportEndpointCache); ok {
			if cached.base == base && cached.agentID == agentID && cached.endpoint != "" {
				return cached.endpoint, nil
			}
		}
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("remote desktop: invalid base URL: %w", err)
	}

	if err := enforceEndpointSecurity(parsed); err != nil {
		return "", err
	}

	pathRef := &url.URL{Path: fmt.Sprintf("/api/agents/%s/remote-desktop/transport", url.PathEscape(agentID))}
	endpoint := parsed.ResolveReference(pathRef).String()
	c.transportCache.Store(transportEndpointCache{base: base, agentID: agentID, endpoint: endpoint})
	return endpoint, nil
}

func enforceEndpointSecurity(u *url.URL) error {
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "":
		u.Scheme = "https"
		scheme = "https"
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("remote desktop: insecure http base URL %q", u.Redacted())
		}
	default:
		return fmt.Errorf("remote desktop: unsupported URL scheme %q", scheme)
	}

	if u.User != nil {
		return errors.New("remote desktop: base URL must not include credentials")
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return errors.New("remote desktop: invalid base URL host")
	}

	return nil
}

func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

package appvncengine

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rootbay/tenvy-client/internal/modules/control/screen"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command       = protocol.Command
	CommandResult = protocol.CommandResult
)

var errSessionReplaced = errors.New("app-vnc session replaced")

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type surfaceFrame struct {
	image  *surfaceImage
	cursor *protocol.AppVncCursorState
}

type surfaceImage struct {
	width  int
	height int
	stride int
	data   []byte
}

type surfaceCapturer interface {
	Capture(ctx context.Context) (*surfaceFrame, error)
	Close() error
}

type surfaceCaptureFactory func(*sessionState) (surfaceCapturer, error)

type frameIntervalFunc func(protocol.AppVncQuality) time.Duration

type Logger interface {
	Printf(format string, args ...interface{})
}

type Config struct {
	Logger         Logger
	WorkspaceRoot  string
	AgentID        string
	BaseURL        string
	AuthKey        string
	Client         HTTPDoer
	UserAgent      string
	RequestTimeout time.Duration
}

type seedDownloadConfig struct {
	BaseURL   string
	AgentID   string
	AuthKey   string
	Client    HTTPDoer
	UserAgent string
	Timeout   time.Duration
}

type Controller struct {
	mu             sync.Mutex
	logger         Logger
	workspaceRoot  string
	agentID        string
	baseURL        string
	authKey        string
	client         HTTPDoer
	userAgent      string
	captureFactory surfaceCaptureFactory
	frameInterval  frameIntervalFunc
	requestTimeout time.Duration
	now            func() time.Time
	processWaiter  func(*Controller, *exec.Cmd, string)
	session        *sessionState
}

type sessionState struct {
	id            string
	workspace     string
	process       *exec.Cmd
	application   *protocol.AppVncApplicationDescriptor
	plan          *protocol.AppVncVirtualizationPlan
	settings      protocol.AppVncSessionSettings
	startedAt     time.Time
	lastBeat      time.Time
	lastSequence  int64
	capture       surfaceCapturer
	captureCancel context.CancelFunc
	captureDone   chan struct{}
	frameSequence int64
	processID     int
}

func NewController() *Controller {
	return &Controller{
		captureFactory: defaultSurfaceCaptureFactory,
		frameInterval:  defaultFrameInterval,
		requestTimeout: 10 * time.Second,
		now:            time.Now,
		processWaiter:  (*Controller).awaitProcess,
	}
}

func (c *Controller) Update(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cfg.Logger != nil {
		c.logger = cfg.Logger
	}
	if root := strings.TrimSpace(cfg.WorkspaceRoot); root != "" {
		c.workspaceRoot = root
	}
	c.agentID = strings.TrimSpace(cfg.AgentID)
	c.baseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	c.authKey = strings.TrimSpace(cfg.AuthKey)
	c.client = cfg.Client
	c.userAgent = strings.TrimSpace(cfg.UserAgent)
	if cfg.RequestTimeout > 0 {
		c.requestTimeout = cfg.RequestTimeout
	}
}

func (c *Controller) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	payload, err := decodePayload(cmd.Payload)
	if err != nil {
		return CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       err.Error(),
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}

	action := strings.ToLower(strings.TrimSpace(payload.Action))
	var actionErr error
	switch action {
	case "start":
		actionErr = c.start(ctx, payload)
	case "stop":
		actionErr = c.stop(payload.SessionID)
	case "configure":
		actionErr = c.configure(payload)
	case "input":
		actionErr = c.handleInput(payload)
	case "heartbeat":
		actionErr = c.heartbeat(payload)
	default:
		actionErr = fmt.Errorf("unsupported app-vnc action: %s", action)
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
		result.Output = fmt.Sprintf("app-vnc %s action processed", action)
	}
	return result
}

func (c *Controller) Shutdown(ctx context.Context) {
	c.mu.Lock()
	session := c.session
	c.session = nil
	c.mu.Unlock()

	if session != nil {
		c.terminateSession(ctx, session, errors.New("shutdown"))
	}
}

func (c *Controller) start(ctx context.Context, payload protocol.AppVncCommandPayload) error {
        sessionID := strings.TrimSpace(payload.SessionID)
        if sessionID == "" {
                return errors.New("missing session identifier")
        }

        c.mu.Lock()
        if c.session != nil && c.session.id == sessionID {
                c.applySettingsLocked(c.session, payload.Settings)
                c.mu.Unlock()
                return nil
        }

        var prev *sessionState
        if c.session != nil {
                prev = c.session
                c.session = nil
        }

        if payload.Application == nil {
                c.mu.Unlock()
                if prev != nil {
                        c.terminateSession(ctx, prev, errSessionReplaced)
                }
                return errors.New("missing application descriptor")
        }

        plan := resolveVirtualizationPlan(payload.Application, payload.Virtualization)
        settings := resolveSettings(payload.Settings)
        workspaceRoot := c.workspaceRoot
        downloadCfg := c.seedDownloadConfigLocked()
        c.mu.Unlock()

        if prev != nil {
                c.terminateSession(ctx, prev, errSessionReplaced)
        }

        if strings.TrimSpace(workspaceRoot) == "" {
                workspaceRoot = os.TempDir()
        }
        workspace, err := os.MkdirTemp(workspaceRoot, "tenvy-appvnc-")
        if err != nil {
                return fmt.Errorf("prepare workspace: %w", err)
        }

        cleanup := func() {
                if removeErr := os.RemoveAll(workspace); removeErr != nil {
                        c.logf("app-vnc: failed to remove workspace %s: %v", workspace, removeErr)
                }
        }
        success := false
        defer func() {
                if !success {
                        cleanup()
                }
        }()

        if plan != nil {
                if err := c.materializeSeed(ctx, plan.ProfileSeed, filepath.Join(workspace, "profile"), "profile", downloadCfg); err != nil {
                        c.logf("app-vnc: profile seed preparation failed: %v", err)
                }
                if err := c.materializeSeed(ctx, plan.DataRoot, filepath.Join(workspace, "data"), "data", downloadCfg); err != nil {
                        c.logf("app-vnc: data root preparation failed: %v", err)
                }
        }

        executable, err := selectExecutable(payload.Application, plan)
        if err != nil {
                return err
        }

        expandedExecutable := expandExecutablePath(executable)
        env := mergeEnvironment(plan)
        cmd := exec.CommandContext(ctx, expandedExecutable) // #nosec G204 - path originates from static descriptor
        cmd.Dir = workspace
        if len(env) > 0 {
                cmd.Env = env
        }

        if err := cmd.Start(); err != nil {
                return fmt.Errorf("launch %s: %w", expandedExecutable, err)
        }

        pid := 0
        if cmd.Process != nil {
                pid = cmd.Process.Pid
        }
        started := c.currentTime()
        state := &sessionState{
                id:          sessionID,
                workspace:   workspace,
                process:     cmd,
                application: payload.Application,
                plan:        plan,
                settings:    settings,
                startedAt:   started,
                lastBeat:    started,
                processID:   pid,
        }

        c.mu.Lock()
        if c.session != nil {
                c.mu.Unlock()
                _ = cmd.Process.Signal(os.Interrupt)
                _ = cmd.Wait()
                return errors.New("app-vnc session replaced")
        }
        c.session = state
        c.startCaptureLocked(state)
        c.mu.Unlock()

        c.logf("app-vnc: session %s started (%s)", sessionID, expandedExecutable)
        waiter := c.processWaiter
        if waiter == nil {
                waiter = (*Controller).awaitProcess
        }
        go waiter(c, cmd, workspace)
        success = true
        return nil
}

func (c *Controller) materializeSeed(
        ctx context.Context,
        reference string,
        target string,
        label string,
        cfg seedDownloadConfig,
) error {
        trimmed := strings.TrimSpace(reference)
        if trimmed == "" {
                return nil
        }
        if err := os.RemoveAll(target); err != nil && !os.IsNotExist(err) {
                return err
        }
        if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "/") {
                if err := c.downloadSeedBundle(ctx, trimmed, target, cfg); err != nil {
                        return err
                }
                c.logf("app-vnc: downloaded %s seed to %s", label, target)
                return nil
        }
	if err := clonePath(trimmed, target); err != nil {
		return err
	}
	c.logf("app-vnc: cloned %s seed from %s", label, trimmed)
	return nil
}

func (c *Controller) seedDownloadConfigLocked() seedDownloadConfig {
        return seedDownloadConfig{
                BaseURL:   c.baseURL,
                AgentID:   c.agentID,
                AuthKey:   c.authKey,
                Client:    c.client,
                UserAgent: c.userAgent,
                Timeout:   c.requestTimeout,
        }
}

func (c *Controller) downloadSeedBundle(ctx context.Context, reference, target string, cfg seedDownloadConfig) error {
        client := cfg.Client
        if client == nil {
                return errors.New("app-vnc: missing http client")
        }

        url, err := resolveSeedURL(reference, cfg.BaseURL)
        if err != nil {
                return err
        }

        timeout := cfg.Timeout
        if timeout <= 0 {
                timeout = 30 * time.Second
        }

        reqCtx := ctx
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(reqCtx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
        if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
                req.Header.Set("User-Agent", ua)
        }
        if key := strings.TrimSpace(cfg.AuthKey); key != "" {
                req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
        }
        if agentID := strings.TrimSpace(cfg.AgentID); agentID != "" {
                req.Header.Set("X-Tenvy-Agent-ID", agentID)
        }
        req.Header.Set("Accept", "application/zip")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("seed download failed: status %d", resp.StatusCode)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(target), "tenvy-seed-*.zip")
	if err != nil {
		return err
	}
	tempName := tempFile.Name()
	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		tempFile.Close()
		os.Remove(tempName)
		return err
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempName)
		return err
	}

	defer os.Remove(tempName)

	if err := extractZipArchive(tempName, target); err != nil {
		return err
	}
	return nil
}

func (c *Controller) startCaptureLocked(session *sessionState) {
	if session == nil || session.captureDone != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.captureCancel = cancel
	session.captureDone = make(chan struct{})
	go func() {
		defer close(session.captureDone)
		c.captureLoop(ctx, session)
	}()
}

func expandExecutablePath(path string) string {
	if path == "" {
		return ""
	}

	expanded := os.ExpandEnv(path)
	if strings.IndexByte(expanded, '%') == -1 {
		return expanded
	}

	var builder strings.Builder
	builder.Grow(len(expanded))
	for i := 0; i < len(expanded); {
		start := strings.IndexByte(expanded[i:], '%')
		if start == -1 {
			builder.WriteString(expanded[i:])
			break
		}
		start += i
		end := strings.IndexByte(expanded[start+1:], '%')
		if end == -1 {
			builder.WriteString(expanded[i:])
			break
		}
		end += start + 1
		name := expanded[start+1 : end]
		builder.WriteString(expanded[i:start])
		if value, ok := os.LookupEnv(name); ok {
			builder.WriteString(value)
		} else {
			builder.WriteString(expanded[start : end+1])
		}
		i = end + 1
	}
	return builder.String()
}

func defaultFrameInterval(quality protocol.AppVncQuality) time.Duration {
	switch quality {
	case protocol.AppVncQualityLossless:
		return 100 * time.Millisecond
	case protocol.AppVncQualityBandwidth:
		return 500 * time.Millisecond
	default:
		return 200 * time.Millisecond
	}
}

func (c *Controller) captureLoop(ctx context.Context, session *sessionState) {
	if session == nil {
		return
	}
	const retryDelay = 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		settings, ok := c.sessionSettings(session)
		if !ok {
			return
		}

		capturer, err := c.ensureSurfaceCapturer(session)
		if err != nil {
			c.logf("app-vnc: capture backend unavailable: %v", err)
			if !c.waitForInterval(ctx, retryDelay) {
				return
			}
			continue
		}
		if capturer == nil {
			if !c.waitForInterval(ctx, retryDelay) {
				return
			}
			continue
		}

		frame, err := capturer.Capture(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !errors.Is(err, context.Canceled) {
				c.logf("app-vnc: capture failed: %v", err)
			}
			if !c.waitForInterval(ctx, retryDelay) {
				return
			}
			continue
		}
		if frame == nil || frame.image == nil || frame.image.width <= 0 || frame.image.height <= 0 || len(frame.image.data) == 0 {
			if !c.waitForInterval(ctx, retryDelay) {
				return
			}
			continue
		}

		encoding, payload, err := encodeSurfaceImage(frame.image, settings.Quality)
		if err != nil {
			c.logf("app-vnc: encode failed: %v", err)
			if !c.waitForInterval(ctx, retryDelay) {
				return
			}
			continue
		}

		packet := protocol.AppVncFramePacket{
			SessionID: session.id,
			Sequence:  atomic.AddInt64(&session.frameSequence, 1),
			Timestamp: c.currentTime().UTC().Format(time.RFC3339Nano),
			Width:     frame.image.width,
			Height:    frame.image.height,
			Encoding:  encoding,
			Image:     base64.StdEncoding.EncodeToString(payload),
		}

		if settings.CaptureCursor && frame.cursor != nil {
			packet.Cursor = frame.cursor
		}
		if meta := c.buildMetadata(settings, session); meta != nil {
			packet.Metadata = meta
		}

		if err := c.sendFramePacket(ctx, packet); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logf("app-vnc: frame delivery failed: %v", err)
		}

		interval := c.frameIntervalForQuality(settings.Quality)
		if !c.waitForInterval(ctx, interval) {
			return
		}
	}
}

func (c *Controller) sessionSettings(session *sessionState) (protocol.AppVncSessionSettings, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != session || session == nil {
		return protocol.AppVncSessionSettings{}, false
	}
	return session.settings, true
}

func (c *Controller) ensureSurfaceCapturer(session *sessionState) (surfaceCapturer, error) {
	c.mu.Lock()
	existing := surfaceCapturer(nil)
	if session != nil {
		existing = session.capture
	}
	factory := c.captureFactory
	c.mu.Unlock()

	if existing != nil {
		return existing, nil
	}

	if factory == nil {
		factory = defaultSurfaceCaptureFactory
	}
	if factory == nil {
		return nil, errors.New("no capture factory configured")
	}
	capturer, err := factory(session)
	if err != nil {
		return nil, err
	}
	if capturer == nil {
		return nil, nil
	}

	c.mu.Lock()
	if session.capture == nil {
		session.capture = capturer
		c.mu.Unlock()
		return capturer, nil
	}
	retained := session.capture
	c.mu.Unlock()

	if err := capturer.Close(); err != nil {
		c.logf("app-vnc: capture factory close error: %v", err)
	}
	return retained, nil
}

func (c *Controller) waitForInterval(ctx context.Context, interval time.Duration) bool {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *Controller) frameIntervalForQuality(quality protocol.AppVncQuality) time.Duration {
	if c != nil && c.frameInterval != nil {
		if interval := c.frameInterval(quality); interval > 0 {
			return interval
		}
	}
	return defaultFrameInterval(quality)
}

func encodeSurfaceImage(img *surfaceImage, quality protocol.AppVncQuality) (string, []byte, error) {
	if img == nil || img.width <= 0 || img.height <= 0 || len(img.data) == 0 {
		return "", nil, errors.New("invalid surface image")
	}

	raw := img.data
	expectedStride := img.width * 4
	if img.stride > 0 && img.stride != expectedStride {
		raw = make([]byte, img.width*img.height*4)
		for y := 0; y < img.height; y++ {
			srcStart := y * img.stride
			srcEnd := srcStart + expectedStride
			dstStart := y * expectedStride
			copy(raw[dstStart:dstStart+expectedStride], img.data[srcStart:srcEnd])
		}
	}

	switch quality {
	case protocol.AppVncQualityLossless:
		data, err := screen.EncodeRGBAAsPNG(img.width, img.height, raw)
		if err != nil {
			return "", nil, err
		}
		return "png", data, nil
	case protocol.AppVncQualityBandwidth:
		data, err := screen.EncodeRGBAAsJPEG(img.width, img.height, 60, raw)
		if err != nil {
			return "", nil, err
		}
		return "jpeg", data, nil
	default:
		data, err := screen.EncodeRGBAAsJPEG(img.width, img.height, 80, raw)
		if err != nil {
			return "", nil, err
		}
		return "jpeg", data, nil
	}
}

func (c *Controller) buildMetadata(settings protocol.AppVncSessionSettings, session *sessionState) *protocol.AppVncSessionMetadata {
	meta := protocol.AppVncSessionMetadata{}
	if trimmed := strings.TrimSpace(settings.AppID); trimmed != "" {
		meta.AppID = trimmed
	}
	if trimmed := strings.TrimSpace(settings.WindowTitle); trimmed != "" {
		meta.WindowTitle = trimmed
	}
	if pid := c.sessionProcessID(session); pid > 0 {
		meta.ProcessID = pid
	}
	if meta == (protocol.AppVncSessionMetadata{}) {
		return nil
	}
	return &meta
}

func (c *Controller) sessionProcessID(session *sessionState) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != session || session == nil {
		return 0
	}
	if session.process != nil && session.process.Process != nil {
		session.processID = session.process.Process.Pid
	}
	return session.processID
}

func (c *Controller) sendFramePacket(ctx context.Context, packet protocol.AppVncFramePacket) error {
	c.mu.Lock()
	baseURL := c.baseURL
	agentID := c.agentID
	authKey := c.authKey
	client := c.client
	userAgent := c.userAgent
	timeout := c.requestTimeout
	c.mu.Unlock()

	trimmedBase := strings.TrimSpace(baseURL)
	if trimmedBase == "" {
		return errors.New("app-vnc: missing base URL")
	}
	trimmedAgent := strings.TrimSpace(agentID)
	if trimmedAgent == "" {
		return errors.New("app-vnc: missing agent identifier")
	}
	if client == nil {
		return errors.New("app-vnc: missing http client")
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/app-vnc/frames", strings.TrimRight(trimmedBase, "/"), url.PathEscape(trimmedAgent))
	data, err := json.Marshal(packet)
	if err != nil {
		return err
	}

	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx := ctx
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(reqCtx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(userAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(authKey); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("frame ingest failed: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Controller) currentTime() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Controller) stop(sessionID string) error {
	id := strings.TrimSpace(sessionID)
	c.mu.Lock()
	session := c.session
	if session != nil && id != "" && session.id != id {
		c.mu.Unlock()
		return errors.New("session identifier mismatch")
	}
	c.session = nil
	c.mu.Unlock()

	if session == nil {
		return nil
	}

	c.terminateSession(context.Background(), session, errors.New("stop"))
	return nil
}

func (c *Controller) configure(payload protocol.AppVncCommandPayload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return errors.New("no active session")
	}
	if id := strings.TrimSpace(payload.SessionID); id != "" && id != c.session.id {
		return errors.New("session identifier mismatch")
	}
	c.applySettingsLocked(c.session, payload.Settings)
	return nil
}

func (c *Controller) handleInput(payload protocol.AppVncCommandPayload) error {
	burst := protocol.AppVncInputBurst{
		SessionID: strings.TrimSpace(payload.SessionID),
		Events:    append([]protocol.AppVncInputEvent(nil), payload.Events...),
	}
	return c.HandleInputBurst(context.Background(), burst)
}

func (c *Controller) heartbeat(payload protocol.AppVncCommandPayload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil
	}
	if id := strings.TrimSpace(payload.SessionID); id != "" && id != c.session.id {
		return errors.New("session identifier mismatch")
	}
	c.session.lastBeat = c.currentTime()
	return nil
}

func (c *Controller) HandleInputBurst(ctx context.Context, burst protocol.AppVncInputBurst) error {
	_ = ctx
	if len(burst.Events) == 0 {
		return nil
	}

	sessionID := strings.TrimSpace(burst.SessionID)
	if sessionID == "" {
		return errors.New("missing session identifier")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session == nil {
		return errors.New("no active session")
	}
	if sessionID != c.session.id {
		return errors.New("session identifier mismatch")
	}

	events := append([]protocol.AppVncInputEvent(nil), burst.Events...)
	c.session.lastSequence = burst.Sequence

	if err := processAppVncInput(ctx, c.session, events); err != nil {
		c.logf("app-vnc: input injection failed: %v", err)
	}

	c.logf("app-vnc: injected %d input events", len(events))
	return nil
}

func (c *Controller) applySettingsLocked(session *sessionState, patch *protocol.AppVncSessionSettingsPatch) {
	if session == nil || patch == nil {
		return
	}
	if patch.Monitor != nil {
		session.settings.Monitor = strings.TrimSpace(*patch.Monitor)
	}
	if patch.Quality != nil {
		session.settings.Quality = *patch.Quality
	}
	if patch.CaptureCursor != nil {
		session.settings.CaptureCursor = *patch.CaptureCursor
	}
	if patch.ClipboardSync != nil {
		session.settings.ClipboardSync = *patch.ClipboardSync
	}
	if patch.BlockLocalInput != nil {
		session.settings.BlockLocalInput = *patch.BlockLocalInput
	}
	if patch.HeartbeatInterval != nil {
		session.settings.HeartbeatInterval = *patch.HeartbeatInterval
	}
	if patch.AppID != nil {
		session.settings.AppID = strings.TrimSpace(*patch.AppID)
	}
	if patch.WindowTitle != nil {
		session.settings.WindowTitle = strings.TrimSpace(*patch.WindowTitle)
	}
}

func (c *Controller) awaitProcess(cmd *exec.Cmd, workspace string) {
	if cmd == nil {
		return
	}
	err := cmd.Wait()
	if err != nil {
		c.logf("app-vnc: process exited with error: %v", err)
	} else {
		c.logf("app-vnc: process exited cleanly")
	}
	if workspace != "" {
		if removeErr := os.RemoveAll(workspace); removeErr != nil {
			c.logf("app-vnc: failed to remove workspace %s after exit: %v", workspace, removeErr)
		}
	}
}

func (c *Controller) terminateSession(ctx context.Context, session *sessionState, reason error) {
	if session == nil {
		return
	}
	if session.captureCancel != nil {
		session.captureCancel()
	}
	if session.captureDone != nil {
		select {
		case <-session.captureDone:
		case <-time.After(2 * time.Second):
		}
	}
	if session.capture != nil {
		if err := session.capture.Close(); err != nil {
			c.logf("app-vnc: capture shutdown failed: %v", err)
		}
		session.capture = nil
	}
	if session.process != nil && session.process.Process != nil {
		_ = session.process.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			session.process.Wait() // nolint: errcheck
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = session.process.Process.Kill()
		}
	}
	if session.workspace != "" {
		if err := os.RemoveAll(session.workspace); err != nil {
			c.logf("app-vnc: cleanup failed for %s: %v", session.workspace, err)
		}
	}
	c.logf("app-vnc: session %s stopped (%v)", session.id, reason)
}

func (c *Controller) logf(format string, args ...interface{}) {
	if c.logger != nil {
		c.logger.Printf(format, args...)
	}
}

func decodePayload(raw json.RawMessage) (protocol.AppVncCommandPayload, error) {
	var payload protocol.AppVncCommandPayload
	if len(raw) == 0 {
		return payload, errors.New("empty command payload")
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return protocol.AppVncCommandPayload{}, fmt.Errorf("invalid app-vnc payload: %w", err)
	}
	return payload, nil
}

func resolveSettings(patch *protocol.AppVncSessionSettingsPatch) protocol.AppVncSessionSettings {
	settings := protocol.AppVncSessionSettings{
		Monitor:           "Primary",
		Quality:           protocol.AppVncQualityBalanced,
		CaptureCursor:     true,
		ClipboardSync:     false,
		BlockLocalInput:   false,
		HeartbeatInterval: 30,
	}
	applySettingsPatch(&settings, patch)
	return settings
}

func applySettingsPatch(target *protocol.AppVncSessionSettings, patch *protocol.AppVncSessionSettingsPatch) {
	if target == nil || patch == nil {
		return
	}
	if patch.Monitor != nil {
		target.Monitor = strings.TrimSpace(*patch.Monitor)
	}
	if patch.Quality != nil {
		target.Quality = *patch.Quality
	}
	if patch.CaptureCursor != nil {
		target.CaptureCursor = *patch.CaptureCursor
	}
	if patch.ClipboardSync != nil {
		target.ClipboardSync = *patch.ClipboardSync
	}
	if patch.BlockLocalInput != nil {
		target.BlockLocalInput = *patch.BlockLocalInput
	}
	if patch.HeartbeatInterval != nil {
		target.HeartbeatInterval = *patch.HeartbeatInterval
	}
	if patch.AppID != nil {
		target.AppID = strings.TrimSpace(*patch.AppID)
	}
	if patch.WindowTitle != nil {
		target.WindowTitle = strings.TrimSpace(*patch.WindowTitle)
	}
}

func resolveVirtualizationPlan(
	descriptor *protocol.AppVncApplicationDescriptor,
	provided *protocol.AppVncVirtualizationPlan,
) *protocol.AppVncVirtualizationPlan {
	if descriptor == nil {
		return provided
	}
	platform := currentPlatform()
	plan := &protocol.AppVncVirtualizationPlan{Platform: platform}
	if provided != nil {
		if provided.Platform != "" {
			plan.Platform = provided.Platform
		}
		plan.ProfileSeed = strings.TrimSpace(provided.ProfileSeed)
		plan.DataRoot = strings.TrimSpace(provided.DataRoot)
		if len(provided.Environment) > 0 {
			plan.Environment = make(map[string]string, len(provided.Environment))
			for key, value := range provided.Environment {
				plan.Environment[key] = value
			}
		}
	}

	hints := descriptor.Virtualization
	if hints != nil {
		if plan.ProfileSeed == "" {
			if value, ok := hints.ProfileSeeds[plan.Platform]; ok {
				plan.ProfileSeed = value
			}
		}
		if plan.DataRoot == "" {
			if value, ok := hints.DataRoots[plan.Platform]; ok {
				plan.DataRoot = value
			}
		}
		if len(plan.Environment) == 0 {
			if env, ok := hints.Environment[plan.Platform]; ok {
				plan.Environment = make(map[string]string, len(env))
				for key, value := range env {
					plan.Environment[key] = value
				}
			}
		}
	}

	plan.ProfileSeed = strings.TrimSpace(plan.ProfileSeed)
	plan.DataRoot = strings.TrimSpace(plan.DataRoot)
	if plan.Environment != nil {
		for key, value := range plan.Environment {
			plan.Environment[key] = strings.TrimSpace(value)
		}
	}
	if plan.ProfileSeed == "" && plan.DataRoot == "" && len(plan.Environment) == 0 {
		if plan.Platform == "" {
			return nil
		}
		return &protocol.AppVncVirtualizationPlan{Platform: plan.Platform}
	}
	return plan
}

func selectExecutable(
	descriptor *protocol.AppVncApplicationDescriptor,
	plan *protocol.AppVncVirtualizationPlan,
) (string, error) {
	if descriptor == nil {
		return "", errors.New("missing application descriptor")
	}
	platform := currentPlatform()
	if plan != nil && plan.Platform != "" {
		platform = plan.Platform
	}
	execPath := ""
	if descriptor.Executable != nil {
		execPath = strings.TrimSpace(descriptor.Executable[platform])
	}
	if execPath == "" {
		return "", fmt.Errorf("no executable configured for %s", platform)
	}
	return execPath, nil
}

func mergeEnvironment(plan *protocol.AppVncVirtualizationPlan) []string {
	overrides := map[string]string{}
	if plan != nil && len(plan.Environment) > 0 {
		for key, value := range plan.Environment {
			if trimmed := strings.TrimSpace(key); trimmed != "" {
				overrides[trimmed] = value
			}
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	current := map[string]string{}
	for _, entry := range os.Environ() {
		if idx := strings.Index(entry, "="); idx > 0 {
			key := entry[:idx]
			current[key] = entry[idx+1:]
		}
	}
	for key, value := range overrides {
		current[key] = value
	}
	env := make([]string, 0, len(current))
	for key, value := range current {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func clonePath(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDirectory(source, destination, info.Mode())
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return copyFile(source, destination, info.Mode())
}

func copyDirectory(source, destination string, mode fs.FileMode) error {
	if err := os.MkdirAll(destination, mode.Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(destination, rel)
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(source, destination string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func resolveSeedURL(reference, base string) (string, error) {
	trimmed := strings.TrimSpace(reference)
	if trimmed == "" {
		return "", errors.New("empty seed reference")
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed, nil
	}
	if strings.HasPrefix(trimmed, "/") {
		trimmedBase := strings.TrimSpace(base)
		if trimmedBase == "" {
			return "", errors.New("missing base URL")
		}
		return strings.TrimRight(trimmedBase, "/") + trimmed, nil
	}
	return "", fmt.Errorf("unsupported seed reference: %s", reference)
}

func extractZipArchive(source, destination string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	if err := os.RemoveAll(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}

	base := filepath.Clean(destination)
	for _, file := range reader.File {
		name := filepath.Clean(file.Name)
		entryPath := filepath.Join(destination, name)
		if !strings.HasPrefix(entryPath, base+string(os.PathSeparator)) && entryPath != base {
			return fmt.Errorf("zip entry escapes destination: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(entryPath, file.Mode().Perm()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(entryPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode().Perm())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		if err := out.Close(); err != nil {
			rc.Close()
			return err
		}
		if err := rc.Close(); err != nil {
			return err
		}
	}
	return nil
}

func currentPlatform() protocol.AppVncPlatform {
	switch runtime.GOOS {
	case "windows":
		return protocol.AppVncPlatformWindows
	case "darwin":
		return protocol.AppVncPlatformMacOS
	default:
		return protocol.AppVncPlatformLinux
	}
}

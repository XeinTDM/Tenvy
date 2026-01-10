package remotedesktopengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/kbinani/screenshot"
	"github.com/vmihailenco/msgpack/v5"
	xdraw "golang.org/x/image/draw"

	"github.com/rootbay/tenvy-client/internal/modules/control/screen"
)

var (
	captureRectFunc             = screen.SafeCaptureRect
	captureCapabilityErrorsFunc = screen.CapabilityErrors
	selectedCaptureBackendFunc  = screen.SelectedBackend
	detectRemoteMonitorsFunc    = detectRemoteMonitors
)

type remoteTileHasher struct {
	tile      int
	cols      int
	rows      int
	width     int
	height    int
	ready     bool
	checksums []uint64
	digest    *xxhash.Digest
}

type streamLoopState struct {
	activeMode     RemoteDesktopStreamMode
	lastSent       time.Time
	clipFrames     []clipFrameBuffer
	clipStart      time.Time
	clipBytes      int
	clipKeyPending bool
	tileHasher     remoteTileHasher
	regionScratch  []tileRegion
	deltaScratch   []RemoteDesktopDeltaRect
	encoderPool    *regionEncoderPool
	clipEncoders   map[string]*clipEncoderState
	ffmpegEnvOnce  sync.Once
	ffmpegEnv      *ffmpegEnvironment
	ffmpegEnvErr   error
	activeClipKind string
}

type clipEncoderState struct {
	encoder clipVideoEncoder
	init    bool
	err     error
	warned  bool
	queued  int
}

type sessionSnapshot struct {
	sessionID       string
	monitor         remoteMonitor
	monitorsPayload []RemoteDesktopMonitorInfo
	width           int
	height          int
	tile            int
	frameInterval   time.Duration
	mode            RemoteDesktopStreamMode
	negotiatedCodec RemoteDesktopEncoder
	activeEncoder   RemoteDesktopEncoder
	forceKey        bool
	minClipQuality  int
	maxClipQuality  int
	clipQuality     int
	sequence        uint64
	previousFrame   []byte
}

func newStreamLoopState(mode RemoteDesktopStreamMode) *streamLoopState {
	state := &streamLoopState{
		activeMode:   mode,
		encoderPool:  sharedRegionEncoderPool(),
		clipEncoders: map[string]*clipEncoderState{},
	}
	if mode == RemoteStreamModeVideo {
		state.clipKeyPending = true
	}
	return state
}

func (s *streamLoopState) ensureFFmpegEnvironment() (*ffmpegEnvironment, error) {
	if s == nil {
		return nil, errors.New("nil stream state")
	}
	s.ffmpegEnvOnce.Do(func() {
		env, err := newFFmpegEnvironment()
		if err != nil {
			s.ffmpegEnvErr = err
			return
		}
		s.ffmpegEnv = env
	})
	if s.ffmpegEnv != nil {
		return s.ffmpegEnv, nil
	}
	return nil, s.ffmpegEnvErr
}

func (s *streamLoopState) resetClipBuffer() {
	if s == nil {
		return
	}
	for _, frame := range s.clipFrames {
		releaseFrameBuffer(frame.Buffer)
	}
	s.clipFrames = nil
	s.clipStart = time.Time{}
	s.clipBytes = 0
	if s.clipEncoders != nil {
		for _, enc := range s.clipEncoders {
			if enc != nil {
				enc.queued = 0
			}
		}
	}
}

func (s *streamLoopState) close() {
	if s == nil {
		return
	}
	s.resetClipBuffer()
	if s.clipEncoders != nil {
		for key, enc := range s.clipEncoders {
			if enc != nil && enc.encoder != nil {
				enc.encoder.Close()
				enc.encoder = nil
			}
			s.clipEncoders[key] = nil
		}
		s.clipEncoders = nil
	}
}

func (s *streamLoopState) encoderState(kind string) *clipEncoderState {
	if s == nil {
		return nil
	}
	if s.clipEncoders == nil {
		s.clipEncoders = map[string]*clipEncoderState{}
	}
	state, ok := s.clipEncoders[kind]
	if !ok || state == nil {
		state = &clipEncoderState{}
		s.clipEncoders[kind] = state
	}
	return state
}

func (s *streamLoopState) ensureClipEncoder(kind string) clipVideoEncoder {
	if s == nil {
		return nil
	}
	state := s.encoderState(kind)
	if state == nil {
		return nil
	}
	if state.init {
		return state.encoder
	}
	var (
		encoder  clipVideoEncoder
		err      error
		provider ffmpegEnvProvider
	)
	if kind == remoteClipEncodingHEVC || kind == remoteClipEncodingH264 {
		provider = func() (*ffmpegEnvironment, error) {
			return s.ensureFFmpegEnvironment()
		}
	}
	switch kind {
	case remoteClipEncodingHEVC:
		encoder, err = newHEVCVideoEncoder(provider)
	case remoteClipEncodingH264:
		encoder, err = newAVCVideoEncoder(provider)
	default:
		err = fmt.Errorf("unsupported clip encoder: %s", kind)
	}
	if err != nil {
		state.init = true
		state.err = err
		state.encoder = nil
		state.queued = 0
		return nil
	}
	state.encoder = encoder
	state.init = true
	state.err = nil
	state.warned = false
	state.queued = 0
	return state.encoder
}

func (s *streamLoopState) disableClipEncoder(kind string, err error) {
	if s == nil {
		return
	}
	state := s.encoderState(kind)
	if state == nil {
		return
	}
	if state.encoder != nil {
		state.encoder.Close()
		state.encoder = nil
	}
	state.init = true
	state.err = err
	state.warned = false
	state.queued = 0
	if s.activeClipKind == kind {
		s.activeClipKind = ""
	}
}

func (s *streamLoopState) queueClipFrame(session *RemoteDesktopSession, snapshot sessionSnapshot, interval time.Duration, frame clipFrameBuffer) {
	if s == nil || session == nil {
		return
	}
	order := clipEncoderOrder(snapshot)
	if len(order) == 0 {
		return
	}
	opts := clipEncodeOptions{
		Width:         frame.Width,
		Height:        frame.Height,
		Quality:       snapshot.clipQuality,
		ForceKey:      s.clipKeyPending && len(s.clipFrames) == 1,
		TargetBitrate: session.TargetBitrateKbps,
		FrameInterval: interval,
		IntraRefresh:  session.IntraRefresh,
	}
	if s.activeClipKind != "" {
		for _, kind := range order {
			if kind == s.activeClipKind {
				if s.queueFrameForKind(kind, frame, opts) == nil {
					return
				}
				break
			}
		}
	}
	for _, kind := range order {
		if kind == s.activeClipKind {
			continue
		}
		if s.queueFrameForKind(kind, frame, opts) == nil {
			s.activeClipKind = kind
			return
		}
	}
}

func (s *streamLoopState) queueFrameForKind(kind string, frame clipFrameBuffer, opts clipEncodeOptions) error {
	state := s.encoderState(kind)
	if state == nil {
		return errors.New("encoder state unavailable")
	}
	if state.init && state.err != nil {
		return state.err
	}
	encoder := s.ensureClipEncoder(kind)
	if encoder == nil {
		if state.err != nil {
			return state.err
		}
		return fmt.Errorf("encoder %s unavailable", kind)
	}
	forceKey := opts.ForceKey
	if err := encoder.QueueFrame(frame, opts, forceKey); err != nil {
		s.disableClipEncoder(kind, err)
		return err
	}
	state.queued++
	state.err = nil
	state.warned = false
	return nil
}

func (s *streamLoopState) queueFramesForKind(kind string, frames []clipFrameBuffer, opts clipEncodeOptions) error {
	state := s.encoderState(kind)
	if state == nil {
		return errors.New("encoder state unavailable")
	}
	if state.init && state.err != nil {
		return state.err
	}
	encoder := s.ensureClipEncoder(kind)
	if encoder == nil {
		if state.err != nil {
			return state.err
		}
		return fmt.Errorf("encoder %s unavailable", kind)
	}
	queued := 0
	requestKey := opts.ForceKey
	for idx, frame := range frames {
		forceKey := requestKey && idx == 0
		if err := encoder.QueueFrame(frame, opts, forceKey); err != nil {
			s.disableClipEncoder(kind, err)
			return err
		}
		queued++
	}
	state.queued = queued
	state.err = nil
	state.warned = false
	return nil
}

func (s *streamLoopState) borrowRegions() []tileRegion {
	if s == nil {
		return nil
	}
	regions := s.regionScratch
	s.regionScratch = nil
	return regions[:0]
}

func (s *streamLoopState) releaseRegions(regions []tileRegion) {
	if s == nil {
		return
	}
	if regions == nil {
		s.regionScratch = nil
		return
	}
	if cap(regions) > maxReusableTileRegions {
		s.regionScratch = nil
		return
	}
	s.regionScratch = regions[:0]
}

func (s *streamLoopState) borrowDeltas(size int) []RemoteDesktopDeltaRect {
	if size <= 0 {
		return nil
	}
	if s == nil {
		return make([]RemoteDesktopDeltaRect, size)
	}
	scratch := s.deltaScratch
	s.deltaScratch = nil
	if cap(scratch) < size {
		scratch = make([]RemoteDesktopDeltaRect, size)
	} else {
		scratch = scratch[:size]
	}
	return scratch
}

func (s *streamLoopState) recycleDeltas(deltas []RemoteDesktopDeltaRect) {
	if s == nil {
		return
	}
	if cap(deltas) > maxReusableDeltaRects {
		for i := range deltas {
			deltas[i] = RemoteDesktopDeltaRect{}
		}
		s.deltaScratch = nil
		return
	}
	for i := range deltas {
		deltas[i] = RemoteDesktopDeltaRect{}
	}
	s.deltaScratch = deltas[:0]
}

func (s *streamLoopState) onModeChange(session *RemoteDesktopSession, nextMode RemoteDesktopStreamMode) {
	if s.activeMode == nextMode {
		return
	}
	s.activeMode = nextMode
	s.resetClipBuffer()
	s.clipKeyPending = nextMode == RemoteStreamModeVideo
	releaseFrameBuffer(session.LastFrame)
	session.LastFrame = nil
	s.tileHasher.reset()
	session.ForceKeyFrame = true
}

func (s *streamLoopState) shouldDropFrame(interval time.Duration) bool {
	if interval <= 0 || s.lastSent.IsZero() {
		return false
	}
	dropThreshold := time.Duration(frameDropBacklogMultiplier) * interval
	return time.Since(s.lastSent) > dropThreshold
}

func (s *streamLoopState) markDropped(interval time.Duration) {
	if s.lastSent.IsZero() {
		s.lastSent = time.Now()
		return
	}
	nextSent := s.lastSent.Add(interval)
	if now := time.Now(); nextSent.After(now) {
		nextSent = now
	}
	s.lastSent = nextSent
}

func (s *streamLoopState) markSent(ts time.Time) {
	s.lastSent = ts
}

func (h *remoteTileHasher) reset() {
	h.tile = 0
	h.cols = 0
	h.rows = 0
	h.width = 0
	h.height = 0
	h.ready = false
	h.checksums = h.checksums[:0]
	if h.digest != nil {
		h.digest.Reset()
	}
}

func (h *remoteTileHasher) ensure(width, height, tile int) {
	if tile <= 0 {
		tile = 32
	}

	cols := (width + tile - 1) / tile
	rows := (height + tile - 1) / tile
	total := cols * rows

	if h.tile != tile || h.cols != cols || h.rows != rows || h.width != width || h.height != height || len(h.checksums) != total {
		if cap(h.checksums) < total {
			h.checksums = make([]uint64, total)
		} else {
			h.checksums = h.checksums[:total]
			clear(h.checksums)
		}
		h.tile = tile
		h.cols = cols
		h.rows = rows
		h.width = width
		h.height = height
		h.ready = false
	}
}

func (h *remoteTileHasher) checksumTile(data []byte, stride, x, y, w, hgt int) uint64 {
	if w <= 0 || hgt <= 0 {
		return 0
	}

	if h.digest == nil {
		h.digest = xxhash.New()
	} else {
		h.digest.Reset()
	}

	rowWidth := w * 4
	base := y*stride + x*4
	for row := 0; row < hgt; row++ {
		start := base + row*stride
		h.digest.Write(data[start : start+rowWidth])
	}

	return h.digest.Sum64()
}

func (h *remoteTileHasher) rebuild(data []byte, width, height, tile int) {
	if data == nil || width <= 0 || height <= 0 {
		h.reset()
		return
	}

	h.ensure(width, height, tile)
	stride := width * 4
	idx := 0
	for y := 0; y < height; y += h.tile {
		hgt := minInt(h.tile, height-y)
		for x := 0; x < width; x += h.tile {
			wdt := minInt(h.tile, width-x)
			h.checksums[idx] = h.checksumTile(data, stride, x, y, wdt, hgt)
			idx++
		}
	}
	h.ready = true
}

const (
	remoteEncodingPNG      = "png"
	remoteEncodingJPEG     = "jpeg"
	remoteEncodingClip     = "clip"
	remoteClipEncodingJPEG = "jpeg"
	remoteClipEncodingHEVC = "hevc"
	remoteClipEncodingH264 = "h264"
	minClipDuration        = 30 * time.Millisecond
	maxClipDuration        = 150 * time.Millisecond
	defaultClipDuration    = 60 * time.Millisecond
	maxClipFrames          = 12
	minClipQuality         = 45
	maxClipQuality         = 92
	defaultClipQuality     = 80
	clipQualityStepDown    = 6
	clipQualityStepUp      = 3
)

const monitorRefreshInterval = 3 * time.Second

var (
	pngEncoder       = png.Encoder{CompressionLevel: png.BestSpeed}
	imageBufferPool  = sync.Pool{New: func() interface{} { return new(bytes.Buffer) }}
	frameBufferPool  = sync.Pool{New: func() interface{} { return make([]byte, 0) }}
	jpegOptionsPool  = sync.Pool{New: func() interface{} { return new(jpeg.Options) }}
	jsonBodyPool     = sync.Pool{New: func() interface{} { return &jsonRequestBody{Buffer: new(bytes.Buffer)} }}
	maxEncodeWorkers = maxInt(1, runtime.GOMAXPROCS(0))
	encoderPoolOnce  sync.Once
	sharedPool       *regionEncoderPool
)

func sharedRegionEncoderPool() *regionEncoderPool {
	encoderPoolOnce.Do(func() {
		if maxEncodeWorkers > 1 {
			sharedPool = newRegionEncoderPool(maxEncodeWorkers)
		}
	})
	return sharedPool
}

type jsonRequestBody struct {
	*bytes.Buffer
}

func acquireJSONBody() *jsonRequestBody {
	value := jsonBodyPool.Get()
	if body, ok := value.(*jsonRequestBody); ok {
		body.Reset()
		return body
	}
	return &jsonRequestBody{Buffer: new(bytes.Buffer)}
}

func releaseJSONBody(body *jsonRequestBody) {
	if body == nil {
		return
	}
	raw := body.Bytes()
	for i := range raw {
		raw[i] = 0
	}
	body.Reset()
	jsonBodyPool.Put(body)
}

const (
	maxDeltaCoverageRatio      = 0.35
	maxDeltaTileFactor         = 3
	jpegKeyFrameMinPixels      = 320 * 240
	jpegDeltaMinPixels         = 32 * 32
	frameDropBacklogMultiplier = 3
	frameDropEMAAlpha          = 0.45
	frameDropRecoveryAlpha     = 0.2
	maxReusableTileRegions     = 2048
	maxReusableDeltaRects      = 2048
)

func (c *remoteDesktopSessionController) stream(ctx context.Context, session *RemoteDesktopSession) {
	defer func() {
		if r := recover(); r != nil {
			c.logf("remote desktop stream panic: %v", r)
		}
	}()
	defer session.wg.Done()
	defer c.closeSessionTransport(session)
	defer func() {
		releaseFrameBuffer(session.LastFrame)
		session.LastFrame = nil
	}()

	timer := time.NewTimer(0)
	defer timer.Stop()

	state := newStreamLoopState(normalizeStreamMode(session.Settings.Mode))
	defer state.close()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		snapshot, ok := c.prepareSnapshot(session, state)
		if !ok {
			return
		}

		interval := snapshot.frameInterval
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}

		if state.shouldDropFrame(interval) {
			c.handleFrameDrop(session, snapshot.mode)
			state.markDropped(interval)
			if snapshot.mode == RemoteStreamModeVideo {
				releaseFrameBuffer(snapshot.previousFrame)
			}
			scheduleNextFrame(timer, interval)
			continue
		}

		if snapshot.mode == RemoteStreamModeVideo {
			nextInterval, sent := c.handleVideoFrame(ctx, session, snapshot, state, interval)
			if nextInterval <= 0 {
				nextInterval = interval
			}
			scheduleNextFrame(timer, nextInterval)
			if sent.IsZero() {
				continue
			}
			state.markSent(sent)
			continue
		}

		nextInterval, sent := c.handleImageFrame(ctx, session, snapshot, state, interval)
		if nextInterval <= 0 {
			nextInterval = interval
		}
		scheduleNextFrame(timer, nextInterval)
		if !sent.IsZero() {
			state.markSent(sent)
		}
	}
}

func (c *remoteDesktopSessionController) prepareSnapshot(session *RemoteDesktopSession, state *streamLoopState) (sessionSnapshot, bool) {
	c.mu.Lock()
	snapshot, ok, stale := c.prepareSnapshotLocked(session, state)
	c.mu.Unlock()
	if !ok {
		releaseFrameBuffer(stale)
	}
	return snapshot, ok
}

func (c *remoteDesktopSessionController) prepareSnapshotLocked(session *RemoteDesktopSession, state *streamLoopState) (sessionSnapshot, bool, []byte) {
	if c.session == nil || c.session.ID != session.ID {
		return sessionSnapshot{}, false, session.LastFrame
	}

	c.refreshMonitorsLocked(session, false)

	monitorIndex := clampMonitorIndex(session.monitors, session.Settings.Monitor)
	session.Settings.Monitor = monitorIndex

	var monitorsPayload []RemoteDesktopMonitorInfo
	if session.monitorsDirty {
		monitorsPayload = append([]RemoteDesktopMonitorInfo(nil), session.monitorInfos...)
	}

	mode := normalizeStreamMode(session.Settings.Mode)
	negotiated := normalizeEncoder(session.NegotiatedCodec)
	activeEncoder := normalizeEncoder(session.ActiveEncoder)
	if negotiated == RemoteEncoderAuto {
		negotiated = activeEncoder
	}
	state.onModeChange(session, mode)

	if mode == RemoteStreamModeVideo {
		_, _ = state.ensureFFmpegEnvironment()
	}

	width := session.Width
	height := session.Height
	tile := session.TileSize
	interval := session.FrameInterval

	minAllowed := session.MinClipQuality
	maxAllowed := session.MaxClipQuality
	if minAllowed <= 0 || minAllowed >= maxAllowed {
		minAllowed = minClipQuality
		maxAllowed = maxClipQuality
	}

	clipQuality := clampInt(session.ClipQuality, minAllowed, maxAllowed)
	if clipQuality <= 0 {
		clipQuality = clampInt(defaultClipQuality, minAllowed, maxAllowed)
	}

	forceKey := session.ForceKeyFrame || len(session.LastFrame) == 0
	previous := session.LastFrame
	sequence := session.Sequence
	if mode == RemoteStreamModeImages {
		sequence++
		session.Sequence = sequence
		session.ForceKeyFrame = false
	}
	if forceKey {
		state.tileHasher.reset()
	}

	snapshot := sessionSnapshot{
		sessionID:       session.ID,
		monitor:         session.monitors[monitorIndex],
		monitorsPayload: monitorsPayload,
		width:           width,
		height:          height,
		tile:            tile,
		frameInterval:   interval,
		mode:            mode,
		negotiatedCodec: negotiated,
		activeEncoder:   activeEncoder,
		forceKey:        forceKey,
		minClipQuality:  minAllowed,
		maxClipQuality:  maxAllowed,
		clipQuality:     clipQuality,
		sequence:        sequence,
		previousFrame:   previous,
	}

	if mode == RemoteStreamModeVideo && len(monitorsPayload) > 0 {
		state.clipKeyPending = true
	}

	return snapshot, true, nil
}

func (c *remoteDesktopSessionController) handleFrameDrop(session *RemoteDesktopSession, mode RemoteDesktopStreamMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil || c.session.ID != session.ID {
		return
	}
	c.session.frameDropEMA = updateEMA(c.session.frameDropEMA, 1, frameDropEMAAlpha)
	if mode == RemoteStreamModeVideo {
		c.session.LastFrame = nil
	}
}

func (c *remoteDesktopSessionController) handleVideoFrame(
	ctx context.Context,
	session *RemoteDesktopSession,
	snapshot sessionSnapshot,
	state *streamLoopState,
	interval time.Duration,
) (time.Duration, time.Time) {
	processStart := time.Now()
	current, err := captureMonitorFrame(snapshot.monitor, snapshot.width, snapshot.height)
	if err != nil {
		c.logf("remote desktop capture error: %v", err)
		c.forceMonitorRefresh(session)
		return interval, time.Time{}
	}
	if ctx.Err() != nil {
		releaseFrameBuffer(current)
		releaseFrameBuffer(snapshot.previousFrame)
		return interval, time.Time{}
	}
	captureDuration := time.Since(processStart)
	encodeDuration := time.Duration(0)

	clipDuration := clampDuration(interval*2, minClipDuration, maxClipDuration)
	if clipDuration <= 0 {
		clipDuration = defaultClipDuration
	}

	if snapshot.forceKey {
		state.resetClipBuffer()
		state.clipStart = time.Now()
		state.clipKeyPending = true
	} else if state.clipStart.IsZero() {
		state.clipStart = time.Now()
	}

	if len(snapshot.monitorsPayload) > 0 {
		state.clipKeyPending = true
	}

	offsetMs := 0
	if !state.clipStart.IsZero() {
		offsetMs = int(time.Since(state.clipStart).Milliseconds())
		if offsetMs < 0 {
			offsetMs = 0
		}
	}

	state.clipFrames = append(state.clipFrames, clipFrameBuffer{
		OffsetMs: offsetMs,
		Width:    snapshot.width,
		Height:   snapshot.height,
		Buffer:   current,
	})

	if len(state.clipFrames) > 0 {
		frame := state.clipFrames[len(state.clipFrames)-1]
		state.queueClipFrame(session, snapshot, interval, frame)
	}

	releaseFrameBuffer(snapshot.previousFrame)

	clipElapsed := time.Since(state.clipStart)
	intervalMs := interval.Milliseconds()
	if intervalMs <= 0 {
		intervalMs = 80
	}
	clipMs := clipDuration.Milliseconds()
	if clipMs <= 0 {
		clipMs = defaultClipDuration.Milliseconds()
	}
	frameCap := int(clipMs/int64(intervalMs)) + 1
	if frameCap < 2 {
		frameCap = 2
	}
	if frameCap > maxClipFrames {
		frameCap = maxClipFrames
	}

	shouldFlush := clipElapsed >= clipDuration || len(state.clipFrames) >= frameCap
	if session.Transport == RemoteTransportWebRTC || session.IntraRefresh {
		shouldFlush = true
	}
	if state.clipKeyPending && len(state.clipFrames) > 0 {
		shouldFlush = true
	}
	if len(snapshot.monitorsPayload) > 0 && len(state.clipFrames) > 0 {
		shouldFlush = true
	}

	if !shouldFlush {
		return interval, time.Time{}
	}

	durationMs := state.clipFrames[len(state.clipFrames)-1].OffsetMs
	if durationMs <= 0 {
		durationMs = int(clipElapsed.Milliseconds())
	}
	if durationMs <= 0 {
		durationMs = int(interval.Milliseconds())
	}

	state.clipBytes = 0
	framesPayload := []RemoteDesktopClipFrame{}
	selectedEncoder := RemoteEncoderAuto

	if snapshot.negotiatedCodec != RemoteEncoderJPEG {
		order := clipEncoderOrder(snapshot)
		if len(order) > 0 {
			selectedEncoder = c.tryClipEncoders(state, session, snapshot, interval, &framesPayload, &encodeDuration, order...)
		}
	}

	if len(framesPayload) == 0 {
		encodeStart := time.Now()
		fallbackFrames, bytesSent, encErr := encodeClipFramesJPEG(state.clipFrames, snapshot.clipQuality)
		encodeDuration += time.Since(encodeStart)
		if encErr != nil {
			c.logf("remote desktop clip encode error: %v", encErr)
			state.resetClipBuffer()
			state.clipKeyPending = true
			return interval, time.Time{}
		}
		framesPayload = fallbackFrames
		state.clipBytes = bytesSent
		selectedEncoder = RemoteEncoderJPEG
		state.activeClipKind = ""
		if session.EncoderHardware == "" {
			session.EncoderHardware = "JPEG"
		}
	}

	if selectedEncoder == RemoteEncoderAuto {
		selectedEncoder = RemoteEncoderHEVC
	}
	c.updateActiveEncoder(session, selectedEncoder)

	processingDuration := time.Since(processStart)
	frameDuration := interval
	if !state.lastSent.IsZero() {
		if elapsed := time.Since(state.lastSent); elapsed > 0 {
			frameDuration = elapsed
		}
	}

	metrics := computeMetrics(interval, frameDuration, captureDuration, encodeDuration, processingDuration, state.clipBytes)
	timestamp := time.Now()
	frame := RemoteDesktopFramePacket{
		SessionID: snapshot.sessionID,
		Timestamp: timestamp.UTC().Format(time.RFC3339Nano),
		Width:     snapshot.width,
		Height:    snapshot.height,
		KeyFrame:  state.clipKeyPending,
		Encoding:  remoteEncodingClip,
		Clip: &RemoteDesktopVideoClip{
			DurationMs: durationMs,
			Frames:     framesPayload,
		},
		Encoder:         selectedEncoder,
		EncoderHardware: session.EncoderHardware,
		IntraRefresh:    session.IntraRefresh,
		Metrics:         metrics,
	}
	if len(framesPayload) > 0 {
		samples := make([]RemoteDesktopMediaSample, len(framesPayload))
		baseTs := timestamp.UTC().UnixMilli()
		for idx, segment := range framesPayload {
			sample := RemoteDesktopMediaSample{
				Kind:      "video",
				Codec:     string(selectedEncoder),
				Timestamp: baseTs + int64(segment.OffsetMs),
				Data:      segment.Data,
				Format:    segment.Encoding,
			}
			if state.clipKeyPending && idx == 0 {
				sample.KeyFrame = true
			}
			samples[idx] = sample
		}
		frame.Media = samples
	}
	if len(snapshot.monitorsPayload) > 0 {
		frame.Monitors = snapshot.monitorsPayload
	}

	nextInterval, ok := c.commitVideoFrame(session, &frame, metrics, processingDuration, frameDuration, state.clipBytes)
	if !ok {
		state.resetClipBuffer()
		state.clipKeyPending = true
		return interval, time.Time{}
	}

	err = c.sendFrame(ctx, session, frame)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			c.logf("remote desktop clip send error: %v", err)
		}
		c.markFrameFailure(session)
		state.resetClipBuffer()
		state.clipKeyPending = true
		return nextInterval, time.Time{}
	}

	state.resetClipBuffer()
	state.clipKeyPending = false
	c.commitVideoFrameSuccess(session, len(snapshot.monitorsPayload) > 0)
	return nextInterval, timestamp
}

func clipEncoderOrder(snapshot sessionSnapshot) []string {
	target := normalizeEncoder(snapshot.negotiatedCodec)
	if target == RemoteEncoderAuto {
		target = normalizeEncoder(snapshot.activeEncoder)
	}

	order := make([]string, 0, 2)
	appendUnique := func(kind string) {
		if kind == "" {
			return
		}
		for _, existing := range order {
			if existing == kind {
				return
			}
		}
		order = append(order, kind)
	}

	switch target {
	case RemoteEncoderJPEG:
		return order
	case RemoteEncoderAVC:
		appendUnique(remoteClipEncodingH264)
		appendUnique(remoteClipEncodingHEVC)
	case RemoteEncoderHEVC:
		appendUnique(remoteClipEncodingHEVC)
		appendUnique(remoteClipEncodingH264)
	default:
		appendUnique(remoteClipEncodingHEVC)
		appendUnique(remoteClipEncodingH264)
	}

	return order
}

func prioritizeEncoderOrder(order []string, active string) []string {
	active = strings.TrimSpace(active)
	if active == "" {
		return order
	}
	idx := -1
	for i, kind := range order {
		if kind == active {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return order
	}
	prioritized := make([]string, 0, len(order))
	prioritized = append(prioritized, active)
	for i, kind := range order {
		if i == idx {
			continue
		}
		prioritized = append(prioritized, kind)
	}
	if len(prioritized) == 0 {
		return order
	}
	seen := make(map[string]bool, len(prioritized))
	unique := make([]string, 0, len(prioritized))
	for _, kind := range prioritized {
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		unique = append(unique, kind)
	}
	if len(unique) == 0 {
		return order
	}
	return unique
}

func (c *remoteDesktopSessionController) tryClipEncoders(
	state *streamLoopState,
	session *RemoteDesktopSession,
	snapshot sessionSnapshot,
	interval time.Duration,
	frames *[]RemoteDesktopClipFrame,
	encodeDuration *time.Duration,
	kinds ...string,
) RemoteDesktopEncoder {
	if len(kinds) == 0 {
		return RemoteEncoderAuto
	}
	opts := clipEncodeOptions{
		Width:         snapshot.width,
		Height:        snapshot.height,
		Quality:       snapshot.clipQuality,
		ForceKey:      state.clipKeyPending,
		TargetBitrate: session.TargetBitrateKbps,
		FrameInterval: interval,
		IntraRefresh:  session.IntraRefresh,
	}
	prioritized := prioritizeEncoderOrder(kinds, state.activeClipKind)
	for _, kind := range prioritized {
		status := state.encoderState(kind)
		if status == nil {
			continue
		}
		if status.init && status.err != nil {
			if !status.warned {
				c.logf("remote desktop %s encoder unavailable: %v", clipEncoderLabel(kind), status.err)
				status.warned = true
			}
			continue
		}
		if status.queued == 0 {
			if err := state.queueFramesForKind(kind, state.clipFrames, opts); err != nil {
				if !status.warned && err != nil {
					c.logf("remote desktop %s encoder unavailable: %v", clipEncoderLabel(kind), err)
					status.warned = true
				}
				continue
			}
		}
		encoder := state.ensureClipEncoder(kind)
		if encoder == nil {
			if status.err != nil && !status.warned {
				c.logf("remote desktop %s encoder unavailable: %v", clipEncoderLabel(kind), status.err)
				status.warned = true
			}
			continue
		}
		encodeStart := time.Now()
		result, encErr := encoder.Flush(state.clipKeyPending)
		*encodeDuration += time.Since(encodeStart)
		status.queued = 0
		if encErr != nil {
			c.logf("remote desktop %s encode failed: %v", clipEncoderLabel(kind), encErr)
			state.disableClipEncoder(kind, encErr)
			continue
		}
		if len(result.Frames) == 0 {
			continue
		}
		state.clipBytes = result.Bytes
		if result.EncoderName != "" {
			session.EncoderHardware = result.EncoderName
		}
		if frames != nil {
			*frames = result.Frames
		}
		state.activeClipKind = kind
		return remoteEncoderFromClipEncoding(result.Encoding)
	}
	return RemoteEncoderAuto
}

func clipEncoderLabel(kind string) string {
	switch kind {
	case remoteClipEncodingHEVC:
		return "hevc"
	case remoteClipEncodingH264:
		return "h264"
	case remoteClipEncodingJPEG:
		return "jpeg"
	default:
		if kind == "" {
			return "unknown"
		}
		return kind
	}
}

func remoteEncoderFromClipEncoding(value string) RemoteDesktopEncoder {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case remoteClipEncodingHEVC:
		return RemoteEncoderHEVC
	case remoteClipEncodingH264:
		return RemoteEncoderAVC
	case remoteClipEncodingJPEG:
		return RemoteEncoderJPEG
	default:
		return RemoteEncoderAuto
	}
}

func (c *remoteDesktopSessionController) commitVideoFrame(
	session *RemoteDesktopSession,
	frame *RemoteDesktopFramePacket,
	metrics *RemoteDesktopFrameMetrics,
	processingDuration, frameDuration time.Duration,
	clipBytes int,
) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session == nil || c.session.ID != session.ID {
		return 0, false
	}

	c.session.Sequence++
	frame.Sequence = c.session.Sequence
	if metrics != nil {
		if c.session.TargetBitrateKbps > 0 {
			metrics.TargetBitrateKbps = float64(c.session.TargetBitrateKbps)
		}
		metrics.LadderLevel = c.session.ladderIndex
	}

	c.maybeAdaptQualityLocked(c.session, metrics, processingDuration, frameDuration, clipBytes)
	c.session.frameDropEMA = updateEMA(c.session.frameDropEMA, 0, frameDropRecoveryAlpha)
	if metrics != nil {
		metrics.FrameLossPercent = math.Round(clampFloat(c.session.frameDropEMA, 0, 1)*1000) / 10
	}
	c.session.ForceKeyFrame = false
	return c.session.FrameInterval, true
}

func (c *remoteDesktopSessionController) handleImageFrame(
	ctx context.Context,
	session *RemoteDesktopSession,
	snapshot sessionSnapshot,
	state *streamLoopState,
	interval time.Duration,
) (time.Duration, time.Time) {
	processStart := time.Now()
	current, err := captureMonitorFrame(snapshot.monitor, snapshot.width, snapshot.height)
	if err != nil {
		c.logf("remote desktop capture error: %v", err)
		c.forceMonitorRefresh(session)
		return interval, time.Time{}
	}
	if ctx.Err() != nil {
		releaseFrameBuffer(current)
		releaseFrameBuffer(snapshot.previousFrame)
		return interval, time.Time{}
	}
	captureDuration := time.Since(processStart)
	encodeDuration := time.Duration(0)

	prev := snapshot.previousFrame
	keyFrame := snapshot.forceKey || len(prev) != len(current) || len(prev) == 0
	var imageData []byte
	var deltas []RemoteDesktopDeltaRect
	var borrowedDeltas []RemoteDesktopDeltaRect
	defer func() {
		if borrowedDeltas != nil {
			state.recycleDeltas(borrowedDeltas)
		}
	}()
	frameEncoding := remoteEncodingPNG
	bytesSent := 0

	if keyFrame {
		encodeStart := time.Now()
		encoded, encoding, err := encodeKeyFrame(snapshot.width, snapshot.height, snapshot.clipQuality, current)
		if err != nil {
			c.logf("remote desktop encode frame: %v", err)
			releaseFrameBuffer(current)
			return interval, time.Time{}
		}
		encodeDuration += time.Since(encodeStart)
		imageData = encoded
		frameEncoding = encoding
		bytesSent += len(encoded)
	} else {
		diffStart := time.Now()
		rects, fallback, err := diffFrames(prev, current, snapshot.width, snapshot.height, snapshot.tile, state, snapshot.clipQuality)
		encodeDuration += time.Since(diffStart)
		if err != nil {
			c.logf("remote desktop diff error: %v", err)
			keyFrame = true
		} else if fallback {
			keyFrame = true
		} else {
			deltas = rects
			if len(rects) > 0 {
				borrowedDeltas = rects
			}
			for _, rect := range rects {
				bytesSent += len(rect.Data)
			}
			if len(rects) == 0 {
				if !state.lastSent.IsZero() && time.Since(state.lastSent) > 3*interval {
					keyFrame = true
				}
			}
		}

		if keyFrame && len(imageData) == 0 {
			encodeStart := time.Now()
			encoded, encoding, encErr := encodeKeyFrame(snapshot.width, snapshot.height, snapshot.clipQuality, current)
			if encErr != nil {
				c.logf("remote desktop fallback encode: %v", encErr)
				releaseFrameBuffer(current)
				return interval, time.Time{}
			}
			imageData = encoded
			frameEncoding = encoding
			bytesSent += len(encoded)
			encodeDuration += time.Since(encodeStart)
		}
	}

	processingDuration := time.Since(processStart)
	frameDuration := interval
	if !state.lastSent.IsZero() {
		if elapsed := time.Since(state.lastSent); elapsed > 0 {
			frameDuration = elapsed
		}
	}

	usedQuality := 0
	if frameEncoding == remoteEncodingJPEG {
		usedQuality = snapshot.clipQuality
	} else if len(deltas) > 0 {
		for _, rect := range deltas {
			if rect.Encoding == remoteEncodingJPEG {
				usedQuality = snapshot.clipQuality
				break
			}
		}
	}

	metrics := computeMetrics(interval, frameDuration, captureDuration, encodeDuration, processingDuration, bytesSent, usedQuality)
	timestamp := time.Now()
	frame := RemoteDesktopFramePacket{
		SessionID:       snapshot.sessionID,
		Sequence:        snapshot.sequence,
		Timestamp:       timestamp.UTC().Format(time.RFC3339Nano),
		Width:           snapshot.width,
		Height:          snapshot.height,
		Encoding:        frameEncoding,
		KeyFrame:        keyFrame,
		Image:           imageData,
		Deltas:          deltas,
		Metrics:         metrics,
		EncoderHardware: session.EncoderHardware,
		IntraRefresh:    session.IntraRefresh,
	}
	if len(snapshot.monitorsPayload) > 0 {
		frame.Monitors = snapshot.monitorsPayload
	}
	if keyFrame {
		frame.Deltas = nil
	}

	if session.EncoderHardware == "" {
		session.EncoderHardware = "CPU"
	}
	nextInterval, ok := c.prepareImageFrameSend(session, metrics, processingDuration, frameDuration, bytesSent)
	if !ok {
		releaseFrameBuffer(current)
		releaseFrameBuffer(prev)
		return interval, time.Time{}
	}

	err = c.sendFrame(ctx, session, frame)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			c.logf("remote desktop frame send error: %v", err)
		}
		c.markFrameFailure(session)
		releaseFrameBuffer(current)
		return nextInterval, time.Time{}
	}

	updatedInterval, updatedTile, updatedWidth, updatedHeight, updated := c.commitImageFrameSuccess(session, snapshot, current, prev)
	if !updated {
		releaseFrameBuffer(current)
		releaseFrameBuffer(prev)
		return nextInterval, timestamp
	}

	if updatedInterval > 0 {
		nextInterval = updatedInterval
	}

	if updatedWidth != snapshot.width || updatedHeight != snapshot.height {
		state.tileHasher.reset()
	} else if keyFrame || updatedTile != snapshot.tile {
		state.tileHasher.rebuild(session.LastFrame, snapshot.width, snapshot.height, updatedTile)
	}

	return nextInterval, timestamp
}

func (c *remoteDesktopSessionController) prepareImageFrameSend(
	session *RemoteDesktopSession,
	metrics *RemoteDesktopFrameMetrics,
	processingDuration, frameDuration time.Duration,
	bytesSent int,
) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session == nil || c.session.ID != session.ID {
		return 0, false
	}

	if metrics != nil {
		if c.session.TargetBitrateKbps > 0 {
			metrics.TargetBitrateKbps = float64(c.session.TargetBitrateKbps)
		}
		metrics.LadderLevel = c.session.ladderIndex
	}
	c.maybeAdaptQualityLocked(c.session, metrics, processingDuration, frameDuration, bytesSent)
	c.session.frameDropEMA = updateEMA(c.session.frameDropEMA, 0, frameDropRecoveryAlpha)
	if metrics != nil {
		metrics.FrameLossPercent = math.Round(clampFloat(c.session.frameDropEMA, 0, 1)*1000) / 10
	}
	return c.session.FrameInterval, true
}

func (c *remoteDesktopSessionController) commitVideoFrameSuccess(session *RemoteDesktopSession, monitorsSynced bool) {
	if session == nil || !monitorsSynced {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session == nil || c.session.ID != session.ID {
		return
	}

	c.session.monitorsDirty = false
	session.monitorsDirty = false
}

func (c *remoteDesktopSessionController) commitImageFrameSuccess(
	session *RemoteDesktopSession,
	snapshot sessionSnapshot,
	current, prev []byte,
) (time.Duration, int, int, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session == nil || c.session.ID != session.ID {
		return 0, 0, 0, 0, false
	}

	c.session.LastFrame = current
	releaseFrameBuffer(prev)
	if len(snapshot.monitorsPayload) > 0 {
		c.session.monitorsDirty = false
	}
	return c.session.FrameInterval, c.session.TileSize, c.session.Width, c.session.Height, true
}

func (c *remoteDesktopSessionController) markFrameFailure(session *RemoteDesktopSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil || c.session.ID != session.ID {
		return
	}
	c.session.frameDropEMA = updateEMA(c.session.frameDropEMA, 1, frameDropEMAAlpha)
}

func (c *remoteDesktopSessionController) forceMonitorRefresh(session *RemoteDesktopSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil || c.session.ID != session.ID {
		return
	}
	c.refreshMonitorsLocked(c.session, true)
}

func (c *remoteDesktopSessionController) sendFrame(ctx context.Context, session *RemoteDesktopSession, frame RemoteDesktopFramePacket) error {
	if session == nil {
		return errors.New("remote desktop: missing session")
	}

	transport := RemoteTransportHTTP
	var sender frameTransport

	c.mu.Lock()
	if c.session != nil && c.session.ID == session.ID {
		if c.session.Transport != "" {
			transport = c.session.Transport
		}
		sender = c.session.transport
	}
	c.mu.Unlock()

	if transport == "" {
		transport = RemoteTransportHTTP
	}
	frame.Transport = transport

	if sender != nil {
		return sender.Send(ctx, frame)
	}

	if transport != RemoteTransportHTTP {
		frame.Transport = RemoteTransportHTTP
	}
	return c.sendFrameHTTP(ctx, frame)
}

func (c *remoteDesktopSessionController) sendFrameHTTP(ctx context.Context, frame RemoteDesktopFramePacket) error {
	cfg := c.config()

	endpoint, err := c.frameEndpoint(cfg)
	if err != nil {
		return err
	}
	client := cfg.Client
	if client == nil {
		return errors.New("remote desktop: missing http client")
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

	encoder := msgpack.NewEncoder(body)
	if err := encoder.Encode(frame); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	req.ContentLength = int64(body.Len())
	req.Header.Set("Content-Type", "application/msgpack")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(c.userAgent()); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if cfg.authHeader != "" {
		req.Header.Set("Authorization", cfg.authHeader)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	drainErr := drainResponseBody(resp.Body)
	if resp.StatusCode >= 300 {
		if drainErr != nil {
			return fmt.Errorf("frame upload failed: status %d: %w", resp.StatusCode, drainErr)
		}
		return fmt.Errorf("frame upload failed: status %d", resp.StatusCode)
	}
	return drainErr
}

func drainResponseBody(body io.Reader) error {
	if body == nil {
		return nil
	}
	const maxDrainBytes int64 = 1 << 20
	limited := &io.LimitedReader{R: body, N: maxDrainBytes + 1}
	n, err := io.Copy(io.Discard, limited)
	if err != nil {
		return err
	}
	if n > maxDrainBytes {
		return fmt.Errorf("response body exceeded %d bytes", maxDrainBytes)
	}
	return nil
}

func captureMonitorFrame(monitor remoteMonitor, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("invalid frame dimensions")
	}

	img, err := captureRectFunc(monitor.bounds)
	if err != nil {
		backend := selectedCaptureBackendFunc()
		if backend == "" {
			if capabilityErrs := captureCapabilityErrorsFunc(); len(capabilityErrs) > 0 {
				return nil, fmt.Errorf("capture unavailable (%v): %w", capabilityErrs[0], err)
			}
			return nil, err
		}
		return nil, fmt.Errorf("%s capture failed: %w", backend, err)
	}
	srcBounds := img.Bounds()
	if srcBounds.Dx() == 0 || srcBounds.Dy() == 0 {
		return nil, errors.New("empty monitor capture")
	}

	frameSize := width * height * 4
	buffer := acquireFrameBuffer(frameSize)

	if srcBounds.Dx() != width || srcBounds.Dy() != height {
		dest := image.RGBA{
			Pix:    buffer,
			Stride: width * 4,
			Rect:   image.Rect(0, 0, width, height),
		}
		xdraw.ApproxBiLinear.Scale(&dest, dest.Rect, img, srcBounds, xdraw.Src, nil)
		return buffer, nil
	}

	copyRGBAInto(buffer, img, width, height)
	return buffer, nil
}

func copyRGBAInto(dst []byte, img *image.RGBA, width, height int) {
	if len(dst) == 0 || img == nil {
		return
	}

	stride := img.Stride
	if stride == width*4 {
		copy(dst, img.Pix[:width*height*4])
		return
	}
	for y := 0; y < height; y++ {
		start := y * width * 4
		rowStart := y * stride
		copy(dst[start:start+width*4], img.Pix[rowStart:rowStart+width*4])
	}
}

func acquireFrameBuffer(size int) []byte {
	if size <= 0 {
		return nil
	}

	value := frameBufferPool.Get()
	if buf, ok := value.([]byte); ok {
		if cap(buf) >= size {
			return buf[:size]
		}
		frameBufferPool.Put(buf[:0])
		return make([]byte, size)
	}

	return make([]byte, size)
}

func releaseFrameBuffer(buf []byte) {
	if buf == nil {
		return
	}
	frameBufferPool.Put(buf[:0])
}

func encodeJPEG(width, height, quality int, data []byte) ([]byte, error) {
	if len(data) == 0 || width <= 0 || height <= 0 {
		return nil, errors.New("invalid frame data")
	}

	img := &image.RGBA{
		Pix:    data,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}
	bufPtr := imageBufferPool.Get().(*bytes.Buffer)
	bufPtr.Reset()
	defer imageBufferPool.Put(bufPtr)

	if quality <= 0 {
		quality = defaultClipQuality
	}
	optsPtr := jpegOptionsPool.Get().(*jpeg.Options)
	optsPtr.Quality = clampInt(quality, 1, 100)
	err := jpeg.Encode(bufPtr, img, optsPtr)
	jpegOptionsPool.Put(optsPtr)
	if err != nil {
		return nil, err
	}

	return append([]byte(nil), bufPtr.Bytes()...), nil
}

func encodeKeyFrame(width, height, quality int, data []byte) ([]byte, string, error) {
	useJPEG := shouldUseJPEGForKeyFrame(width, height, quality)
	if useJPEG {
		if encoded, err := encodeJPEG(width, height, quality, data); err == nil {
			return encoded, remoteEncodingJPEG, nil
		}
	}

	encoded, err := screen.EncodeRGBAAsPNG(width, height, data)
	if err != nil {
		return nil, remoteEncodingPNG, err
	}
	return encoded, remoteEncodingPNG, nil
}

func shouldUseJPEGForKeyFrame(width, height, quality int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	area := width * height
	if area >= jpegKeyFrameMinPixels {
		return true
	}
	if quality <= 0 {
		quality = defaultClipQuality
	}
	if quality >= 85 && area >= 240*180 {
		return true
	}
	return false
}

func encodeRegionPNG(data []byte, stride, x, y, w, h int) ([]byte, error) {
	if stride <= 0 || w <= 0 || h <= 0 {
		return nil, errors.New("invalid region dimensions")
	}

	start := y*stride + x*4
	if start < 0 || start >= len(data) {
		return nil, errors.New("region start out of range")
	}

	needed := (h-1)*stride + w*4
	if start+needed > len(data) {
		return nil, errors.New("region exceeds frame bounds")
	}

	region := image.RGBA{
		Pix:    data[start : start+needed],
		Stride: stride,
		Rect:   image.Rect(0, 0, w, h),
	}

	bufPtr := imageBufferPool.Get().(*bytes.Buffer)
	bufPtr.Reset()
	defer imageBufferPool.Put(bufPtr)

	if err := pngEncoder.Encode(bufPtr, &region); err != nil {
		return nil, err
	}

	return append([]byte(nil), bufPtr.Bytes()...), nil
}

func encodeRegionJPEG(data []byte, stride, x, y, w, h, quality int) ([]byte, error) {
	if stride <= 0 || w <= 0 || h <= 0 {
		return nil, errors.New("invalid region dimensions")
	}

	start := y*stride + x*4
	if start < 0 || start >= len(data) {
		return nil, errors.New("region start out of range")
	}

	needed := (h-1)*stride + w*4
	if start+needed > len(data) {
		return nil, errors.New("region exceeds frame bounds")
	}

	region := image.RGBA{
		Pix:    data[start : start+needed],
		Stride: stride,
		Rect:   image.Rect(0, 0, w, h),
	}

	bufPtr := imageBufferPool.Get().(*bytes.Buffer)
	bufPtr.Reset()
	defer imageBufferPool.Put(bufPtr)

	if quality <= 0 {
		quality = defaultClipQuality
	}
	optsPtr := jpegOptionsPool.Get().(*jpeg.Options)
	optsPtr.Quality = clampInt(quality, 1, 100)
	err := jpeg.Encode(bufPtr, &region, optsPtr)
	jpegOptionsPool.Put(optsPtr)
	if err != nil {
		return nil, err
	}

	return append([]byte(nil), bufPtr.Bytes()...), nil
}

func shouldUseJPEGForRegion(width, height, quality int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	area := width * height
	if area >= jpegDeltaMinPixels {
		return true
	}
	if quality <= 0 {
		quality = defaultClipQuality
	}
	if quality >= 85 && area >= 24*24 {
		return true
	}
	return false
}

func encodeTileRegion(data []byte, stride int, region tileRegion, quality int) (RemoteDesktopDeltaRect, error) {
	preferJPEG := shouldUseJPEGForRegion(region.w, region.h, quality)
	if preferJPEG {
		if encoded, err := encodeRegionJPEG(data, stride, region.x, region.y, region.w, region.h, quality); err == nil {
			return RemoteDesktopDeltaRect{
				X:        region.x,
				Y:        region.y,
				Width:    region.w,
				Height:   region.h,
				Encoding: remoteEncodingJPEG,
				Data:     encoded,
			}, nil
		}
	}

	encoded, err := encodeRegionPNG(data, stride, region.x, region.y, region.w, region.h)
	if err != nil {
		return RemoteDesktopDeltaRect{}, err
	}

	return RemoteDesktopDeltaRect{
		X:        region.x,
		Y:        region.y,
		Width:    region.w,
		Height:   region.h,
		Encoding: remoteEncodingPNG,
		Data:     encoded,
	}, nil
}

type tileRegion struct {
	x int
	y int
	w int
	h int
}

func diffFrames(previous, current []byte, width, height, tile int, loop *streamLoopState, quality int) ([]RemoteDesktopDeltaRect, bool, error) {
	if loop == nil {
		return diffFramesLegacy(previous, current, width, height, tile, quality)
	}

	if len(previous) != len(current) {
		return nil, false, errors.New("frame size mismatch")
	}
	if len(current) == 0 {
		return nil, false, nil
	}

	if bytes.Equal(previous, current) {
		loop.tileHasher.ready = true
		return nil, false, nil
	}

	state := &loop.tileHasher
	state.ensure(width, height, tile)
	stride := width * 4
	baselineReady := state.ready

	estimatedCols := (width + tile - 1) / tile
	estimatedRows := (height + tile - 1) / tile
	totalTiles := maxInt(1, estimatedCols*estimatedRows)
	requiredCapacity := maxInt(1, totalTiles/4)
	regions := loop.borrowRegions()
	if cap(regions) < requiredCapacity {
		loop.releaseRegions(regions)
		regions = make([]tileRegion, 0, requiredCapacity)
	}
	regions = regions[:0]
	defer func() {
		loop.releaseRegions(regions)
	}()

	maxRegions := maxInt(64, totalTiles/maxDeltaTileFactor)
	maxPixels := int(float64(width*height) * maxDeltaCoverageRatio)
	if maxPixels <= 0 {
		maxPixels = width * height
	}

	changedPixels := 0
	idx := 0
	for y := 0; y < height; y += tile {
		h := minInt(tile, height-y)
		for x := 0; x < width; x += tile {
			w := minInt(tile, width-x)
			tileIndex := idx
			idx++

			prevSum := uint64(0)
			if tileIndex < len(state.checksums) {
				prevSum = state.checksums[tileIndex]
			}

			sum := state.checksumTile(current, stride, x, y, w, h)
			state.checksums[tileIndex] = sum

			if baselineReady && prevSum == sum {
				continue
			}

			tileArea := w * h
			regions = append(regions, tileRegion{x: x, y: y, w: w, h: h})
			changedPixels += tileArea
			if len(regions) > maxRegions || changedPixels > maxPixels {
				state.ready = false
				return nil, true, nil
			}
		}
	}

	if len(regions) == 0 {
		state.ready = true
		return nil, false, nil
	}

	regions = mergeTileRegions(regions)

	dest := loop.borrowDeltas(len(regions))
	deltas, err := encodeRegions(current, stride, regions, quality, dest, loop.encoderPool)
	if err != nil {
		loop.recycleDeltas(dest)
		state.ready = false
		return nil, false, err
	}

	state.ready = true
	return deltas, false, nil
}

func diffFramesLegacy(previous, current []byte, width, height, tile int, quality int) ([]RemoteDesktopDeltaRect, bool, error) {
	if len(previous) != len(current) {
		return nil, false, errors.New("frame size mismatch")
	}
	if len(current) == 0 {
		return nil, false, nil
	}

	if bytes.Equal(previous, current) {
		return nil, false, nil
	}

	if tile <= 0 {
		tile = 32
	}

	stride := width * 4
	estimatedCols := (width + tile - 1) / tile
	estimatedRows := (height + tile - 1) / tile
	totalTiles := maxInt(1, estimatedCols*estimatedRows)
	regions := make([]tileRegion, 0, maxInt(1, totalTiles/4))

	maxRegions := maxInt(64, totalTiles/maxDeltaTileFactor)
	maxPixels := int(float64(width*height) * maxDeltaCoverageRatio)
	if maxPixels <= 0 {
		maxPixels = width * height
	}

	changedPixels := 0

	for y := 0; y < height; y += tile {
		h := minInt(tile, height-y)
		for x := 0; x < width; x += tile {
			w := minInt(tile, width-x)
			if regionChanged(previous, current, stride, x, y, w, h) {
				regions = append(regions, tileRegion{x: x, y: y, w: w, h: h})
				changedPixels += w * h
				if len(regions) > maxRegions || changedPixels > maxPixels {
					return nil, true, nil
				}
			}
		}
	}

	if len(regions) == 0 {
		return nil, false, nil
	}

	regions = mergeTileRegions(regions)

	deltas, err := encodeRegions(current, stride, regions, quality, nil, nil)
	if err != nil {
		return nil, false, err
	}
	return deltas, false, nil
}

func mergeTileRegions(regions []tileRegion) []tileRegion {
	if len(regions) <= 1 {
		return regions
	}

	merged := regions[:0]
	current := regions[0]
	for i := 1; i < len(regions); i++ {
		next := regions[i]
		if next.y == current.y && next.h == current.h && next.x == current.x+current.w {
			current.w += next.w
			continue
		}
		merged = append(merged, current)
		current = next
	}
	merged = append(merged, current)
	return merged
}

type regionEncoderPool struct {
	jobs chan regionEncodeTask
}

type regionEncodeTask struct {
	index   int
	region  tileRegion
	data    []byte
	stride  int
	quality int
	dest    []RemoteDesktopDeltaRect
	wg      *sync.WaitGroup
	errCh   chan<- error
	state   *regionEncodeState
}

type regionEncodeState struct {
	aborted atomic.Bool
}

func newRegionEncoderPool(workerCount int) *regionEncoderPool {
	if workerCount <= 1 {
		return nil
	}
	pool := &regionEncoderPool{
		jobs: make(chan regionEncodeTask, workerCount*2),
	}
	for i := 0; i < workerCount; i++ {
		go pool.worker()
	}
	return pool
}

func (p *regionEncoderPool) submit(task regionEncodeTask) {
	if p == nil {
		task.run()
		return
	}
	p.jobs <- task
}

func (p *regionEncoderPool) worker() {
	for task := range p.jobs {
		task.run()
	}
}

func (t regionEncodeTask) run() {
	if t.wg == nil {
		return
	}
	if t.state != nil && t.state.aborted.Load() {
		t.wg.Done()
		return
	}
	rect, err := encodeTileRegion(t.data, t.stride, t.region, t.quality)
	if err != nil {
		if t.state != nil {
			t.state.aborted.Store(true)
		}
		if t.errCh != nil {
			select {
			case t.errCh <- err:
			default:
			}
		}
	} else {
		t.dest[t.index] = rect
	}
	t.wg.Done()
}

func encodeRegions(data []byte, stride int, regions []tileRegion, quality int, scratch []RemoteDesktopDeltaRect, pool *regionEncoderPool) ([]RemoteDesktopDeltaRect, error) {
	if len(regions) == 0 {
		if scratch == nil {
			return nil, nil
		}
		return scratch[:0], nil
	}

	if cap(scratch) < len(regions) {
		scratch = make([]RemoteDesktopDeltaRect, len(regions))
	} else {
		scratch = scratch[:len(regions)]
	}

	workerCount := minInt(len(regions), maxEncodeWorkers)
	if workerCount <= 1 || pool == nil {
		for idx, region := range regions {
			rect, err := encodeTileRegion(data, stride, region, quality)
			if err != nil {
				return nil, err
			}
			scratch[idx] = rect
		}
		return scratch, nil
	}

	var wg sync.WaitGroup
	state := &regionEncodeState{}
	errCh := make(chan error, 1)

	for idx, region := range regions {
		wg.Add(1)
		pool.submit(regionEncodeTask{
			index:   idx,
			region:  region,
			data:    data,
			stride:  stride,
			quality: quality,
			dest:    scratch,
			wg:      &wg,
			errCh:   errCh,
			state:   state,
		})
	}

	wg.Wait()

	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	return scratch, nil
}

func regionChanged(prev, curr []byte, stride, x, y, w, h int) bool {
	for row := 0; row < h; row++ {
		start := (y+row)*stride + x*4
		end := start + w*4
		if !bytes.Equal(prev[start:end], curr[start:end]) {
			return true
		}
	}
	return false
}

func (c *remoteDesktopSessionController) refreshMonitorsLocked(session *RemoteDesktopSession, force bool) {
	if session == nil {
		return
	}

	if !force && !session.lastMonitorRefresh.IsZero() {
		if time.Since(session.lastMonitorRefresh) < monitorRefreshInterval {
			if len(session.monitorInfos) == 0 && len(session.monitors) > 0 {
				session.monitorInfos = monitorInfos(session.monitors)
			}
			return
		}
	}

	monitors := detectRemoteMonitorsFunc()
	if len(monitors) == 0 {
		rect := image.Rect(0, 0, 1280, 720)
		monitors = []remoteMonitor{{
			info:   RemoteDesktopMonitorInfo{ID: 0, Label: "Primary", Width: rect.Dx(), Height: rect.Dy()},
			bounds: rect,
		}}
	}

	now := time.Now()
	if !force && monitorsEquivalent(session.monitors, monitors) {
		session.lastMonitorRefresh = now
		if len(session.monitorInfos) == 0 {
			session.monitorInfos = monitorInfos(monitors)
		}
		return
	}

	session.monitors = monitors
	session.monitorInfos = monitorInfos(monitors)
	session.Settings.Monitor = clampMonitorIndex(monitors, session.Settings.Monitor)
	session.monitorsDirty = true
	session.LastFrame = nil
	session.ForceKeyFrame = true
	session.lastMonitorRefresh = now
}

func monitorInfos(monitors []remoteMonitor) []RemoteDesktopMonitorInfo {
	infos := make([]RemoteDesktopMonitorInfo, len(monitors))
	for i, monitor := range monitors {
		infos[i] = monitor.info
	}
	return infos
}

func monitorInfoAt(session *RemoteDesktopSession, index int) RemoteDesktopMonitorInfo {
	if session == nil {
		return RemoteDesktopMonitorInfo{ID: 0, Label: "Primary", Width: 1280, Height: 720}
	}
	if index >= 0 && index < len(session.monitorInfos) {
		return session.monitorInfos[index]
	}
	if len(session.monitorInfos) > 0 {
		return session.monitorInfos[0]
	}
	width := session.NativeWidth
	height := session.NativeHeight
	if width <= 0 {
		width = session.BaseWidth
	}
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = session.BaseHeight
	}
	if height <= 0 {
		height = 720
	}
	return RemoteDesktopMonitorInfo{ID: 0, Label: "Primary", Width: width, Height: height}
}

func detectRemoteMonitors() []remoteMonitor {
	count := safeNumDisplays()
	monitors := make([]remoteMonitor, 0, count)
	for i := 0; i < count; i++ {
		bounds, ok := safeGetDisplayBounds(i)
		if !ok {
			continue
		}
		width := bounds.Dx()
		height := bounds.Dy()
		if width <= 0 || height <= 0 {
			continue
		}
		info := RemoteDesktopMonitorInfo{
			ID:     i,
			Label:  fmt.Sprintf("Display %d", i+1),
			Width:  width,
			Height: height,
		}
		monitors = append(monitors, remoteMonitor{info: info, bounds: bounds})
	}
	return monitors
}

func safeNumDisplays() (count int) {
	defer func() {
		if r := recover(); r != nil {
			count = 0
		}
	}()
	count = screenshot.NumActiveDisplays()
	return
}

func safeGetDisplayBounds(index int) (rect image.Rectangle, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			rect = image.Rectangle{}
			ok = false
		}
	}()
	rect = screenshot.GetDisplayBounds(index)
	ok = true
	return
}

func monitorsEquivalent(a, b []remoteMonitor) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].info.ID != b[i].info.ID {
			return false
		}
		if a[i].info.Width != b[i].info.Width || a[i].info.Height != b[i].info.Height {
			return false
		}
	}
	return true
}

func clampMonitorIndex(monitors []remoteMonitor, index int) int {
	if len(monitors) == 0 {
		return 0
	}
	if index < 0 || index >= len(monitors) {
		return 0
	}
	return index
}

func computeMetrics(
	targetInterval, frameDuration, captureDuration, encodeDuration, processing time.Duration,
	bytesSent int,
	_ ...int,
) *RemoteDesktopFrameMetrics {
	if targetInterval <= 0 {
		targetInterval = 100 * time.Millisecond
	}
	if frameDuration <= 0 {
		frameDuration = targetInterval
	}

	fps := 0.0
	if frameDuration > 0 {
		fps = 1.0 / frameDuration.Seconds()
	}
	bandwidth := 0.0
	if frameDuration > 0 {
		bandwidth = float64(bytesSent*8) / 1024 / frameDuration.Seconds()
	}

	metrics := &RemoteDesktopFrameMetrics{
		FPS:           math.Round(fps*10) / 10,
		BandwidthKbps: math.Round(bandwidth*10) / 10,
	}

	if captureDuration > 0 {
		metrics.CaptureLatencyMs = roundDurationMillis(captureDuration)
	}
	if encodeDuration > 0 {
		metrics.EncodeLatencyMs = roundDurationMillis(encodeDuration)
	}
	if processing > 0 {
		metrics.ProcessingLatencyMs = roundDurationMillis(processing)
	}

	jitter := math.Abs(frameDuration.Seconds()-targetInterval.Seconds()) * 1000
	if jitter > 0 {
		metrics.FrameJitterMs = math.Round(jitter*10) / 10
	}

	return metrics
}

func scheduleNextFrame(timer *time.Timer, interval time.Duration) {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(interval)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(value, min, max int) int {
	if max < min {
		min, max = max, min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func clampFloat(value, min, max float64) float64 {
	if max < min {
		min, max = max, min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func updateEMA(current, sample, alpha float64) float64 {
	if alpha <= 0 {
		return current
	}
	if alpha >= 1 {
		if sample < 0 {
			return 0
		}
		return sample
	}
	if sample < 0 {
		sample = 0
	}
	if current <= 0 {
		return sample
	}
	return current*(1-alpha) + sample*alpha
}

func clampDuration(value, min, max time.Duration) time.Duration {
	if max < min {
		min, max = max, min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func roundDurationMillis(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	ms := d.Seconds() * 1000
	return math.Round(ms*10) / 10
}

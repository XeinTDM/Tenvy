package remotedesktopengine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type clipFrameBuffer struct {
	OffsetMs int
	Width    int
	Height   int
	Buffer   []byte
}

type clipVideoEncoder interface {
	QueueFrame(frame clipFrameBuffer, opts clipEncodeOptions, forceKey bool) error
	Flush(forceKey bool) (clipEncodeResult, error)
	Close() error
}

type clipEncodeOptions struct {
	Width         int
	Height        int
	Quality       int
	ForceKey      bool
	TargetBitrate int
	FrameInterval time.Duration
	IntraRefresh  bool
}

type clipEncodeResult struct {
	Frames      []RemoteDesktopClipFrame
	Bytes       int
	Encoding    string
	EncoderName string
}

type ClipEncoderEvent struct {
	Encoder   string
	Candidate string
	Event     string
	Frames    int
	Bytes     int
	Duration  time.Duration
	Err       error
}

type ClipEncoderProfiler interface {
	RecordClipEncoderEvent(event ClipEncoderEvent)
}

type clipEncoderProfilerFunc func(event ClipEncoderEvent)

func (f clipEncoderProfilerFunc) RecordClipEncoderEvent(event ClipEncoderEvent) {
	f(event)
}

var (
	clipEncoderProfiler     ClipEncoderProfiler = clipEncoderProfilerFunc(func(ClipEncoderEvent) {})
	clipEncoderProfilerLock sync.RWMutex
)

func SetClipEncoderProfiler(prof ClipEncoderProfiler) {
	clipEncoderProfilerLock.Lock()
	if prof == nil {
		clipEncoderProfiler = clipEncoderProfilerFunc(func(ClipEncoderEvent) {})
	} else {
		clipEncoderProfiler = prof
	}
	clipEncoderProfilerLock.Unlock()
}

func recordClipEncoderEvent(event ClipEncoderEvent) {
	clipEncoderProfilerLock.RLock()
	profiler := clipEncoderProfiler
	clipEncoderProfilerLock.RUnlock()
	profiler.RecordClipEncoderEvent(event)
}

var (
	ErrNativeEncoderUnavailable = errors.New("native encoder unavailable")

	nativeHEVCFactory = platformNewNativeHEVCVideoEncoder
	nativeAVCFactory  = platformNewNativeAVCVideoEncoder

	ffmpegHEVCFactory = newFFmpegHEVCVideoEncoder
	ffmpegAVCFactory  = newFFmpegAVCVideoEncoder
)

type ffmpegEnvProvider func() (*ffmpegEnvironment, error)

type ffmpegClipEncoder struct {
	env        *ffmpegEnvironment
	container  string
	candidates []ffmpegEncoderCandidate

	mu               sync.Mutex
	worker           *ffmpegEncoderWorker
	lastCandidate    ffmpegEncoderCandidate
	failedCandidates map[string]error
}

type ffmpegEnvironment struct {
	path string
	caps *ffmpegEncoderCapabilities
}

type ffmpegEncoderCandidate struct {
	name      string
	encoder   string
	filter    string
	extraArgs []string
}

type clipWorkerConfig struct {
	width        int
	height       int
	bitrate      int
	gop          int
	fps          float64
	intraRefresh bool
}

type ffmpegEncoderWorker struct {
	candidate ffmpegEncoderCandidate
	container string
	config    clipWorkerConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr bytes.Buffer

	mu        sync.Mutex
	buffer    []byte
	keyframes []int
	notify    chan struct{}
	closed    bool
	err       error
	parser    *annexBNALParser
	closeOnce sync.Once
}

type annexBNALParser struct {
	codec string
	tail  []byte
}

func newFFmpegEnvironment() (*ffmpegEnvironment, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg binary not found: %w", err)
	}
	caps := detectFFmpegEncoderCapabilities(path)
	return &ffmpegEnvironment{path: path, caps: caps}, nil
}

func newHEVCVideoEncoder(provider ffmpegEnvProvider) (clipVideoEncoder, error) {
	start := time.Now()
	encoder, err := nativeHEVCFactory()
	if err == nil {
		recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingHEVC, Candidate: "native", Event: "init", Duration: time.Since(start)})
		return encoder, nil
	}
	recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingHEVC, Candidate: "native", Event: "init", Duration: time.Since(start), Err: err})

	encoder, ferr := ffmpegHEVCFactory(provider)
	duration := time.Since(start)
	if ferr != nil {
		recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingHEVC, Candidate: "ffmpeg", Event: "init", Duration: duration, Err: ferr})
		return nil, ferr
	}
	recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingHEVC, Candidate: "ffmpeg", Event: "init", Duration: duration})
	return encoder, nil
}

func newAVCVideoEncoder(provider ffmpegEnvProvider) (clipVideoEncoder, error) {
	start := time.Now()
	encoder, err := nativeAVCFactory()
	if err == nil {
		recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingH264, Candidate: "native", Event: "init", Duration: time.Since(start)})
		return encoder, nil
	}
	recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingH264, Candidate: "native", Event: "init", Duration: time.Since(start), Err: err})

	encoder, ferr := ffmpegAVCFactory(provider)
	duration := time.Since(start)
	if ferr != nil {
		recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingH264, Candidate: "ffmpeg", Event: "init", Duration: duration, Err: ferr})
		return nil, ferr
	}
	recordClipEncoderEvent(ClipEncoderEvent{Encoder: remoteClipEncodingH264, Candidate: "ffmpeg", Event: "init", Duration: duration})
	return encoder, nil
}

func resolveFFmpegEnvironment(provider ffmpegEnvProvider) (*ffmpegEnvironment, error) {
	if provider != nil {
		return provider()
	}
	return newFFmpegEnvironment()
}

func newFFmpegHEVCVideoEncoder(provider ffmpegEnvProvider) (clipVideoEncoder, error) {
	env, err := resolveFFmpegEnvironment(provider)
	if err != nil {
		return nil, err
	}
	caps := env.caps
	candidates := make([]ffmpegEncoderCandidate, 0, 5)
	if caps.supports("hevc_nvenc") {
		candidates = append(candidates, ffmpegEncoderCandidate{
			name:    "NVENC",
			encoder: "hevc_nvenc",
			filter:  "format=bgra,hwupload,scale_npp=format=yuv420p",
		})
	}
	if caps.supports("hevc_qsv") {
		candidates = append(candidates, ffmpegEncoderCandidate{
			name:    "QuickSync",
			encoder: "hevc_qsv",
			filter:  "format=bgra,hwupload,scale_qsv=format=nv12",
		})
	}
	if caps.supports("hevc_amf") {
		candidates = append(candidates, ffmpegEncoderCandidate{name: "AMF", encoder: "hevc_amf", filter: "format=yuv420p"})
	}
	if caps.supports("hevc_vaapi") {
		candidates = append(candidates, ffmpegEncoderCandidate{
			name:    "VA-API",
			encoder: "hevc_vaapi",
			filter:  "format=bgra,hwupload,scale_vaapi=format=nv12",
			extraArgs: []string{
				"-profile:v", "main",
			},
		})
	}
	if caps.supports("libx265") {
		candidates = append(candidates, ffmpegEncoderCandidate{name: "libx265", encoder: "libx265", filter: "format=yuv420p", extraArgs: []string{"-preset", "faster"}})
	} else if len(candidates) == 0 {
		candidates = append(candidates, ffmpegEncoderCandidate{name: "libx265", encoder: "libx265", filter: "format=yuv420p", extraArgs: []string{"-preset", "faster"}})
	}
	return &ffmpegClipEncoder{
		env:              env,
		container:        "hevc",
		candidates:       candidates,
		failedCandidates: map[string]error{},
	}, nil
}

func newFFmpegAVCVideoEncoder(provider ffmpegEnvProvider) (clipVideoEncoder, error) {
	env, err := resolveFFmpegEnvironment(provider)
	if err != nil {
		return nil, err
	}
	caps := env.caps
	candidates := make([]ffmpegEncoderCandidate, 0, 6)
	if caps.supports("h264_nvenc") {
		candidates = append(candidates, ffmpegEncoderCandidate{
			name:    "NVENC",
			encoder: "h264_nvenc",
			filter:  "format=bgra,hwupload,scale_npp=format=yuv420p",
		})
	}
	if caps.supports("h264_qsv") {
		candidates = append(candidates, ffmpegEncoderCandidate{
			name:    "QuickSync",
			encoder: "h264_qsv",
			filter:  "format=bgra,hwupload,scale_qsv=format=nv12",
		})
	}
	if caps.supports("h264_amf") {
		candidates = append(candidates, ffmpegEncoderCandidate{name: "AMF", encoder: "h264_amf", filter: "format=yuv420p"})
	}
	if caps.supports("h264_vaapi") {
		candidates = append(candidates, ffmpegEncoderCandidate{
			name:    "VA-API",
			encoder: "h264_vaapi",
			filter:  "format=bgra,hwupload,scale_vaapi=format=nv12",
			extraArgs: []string{
				"-profile:v", "high",
			},
		})
	}
	if caps.supports("libx264") {
		candidates = append(candidates, ffmpegEncoderCandidate{name: "libx264", encoder: "libx264", filter: "format=yuv420p", extraArgs: []string{"-preset", "fast"}})
	} else if len(candidates) == 0 {
		candidates = append(candidates, ffmpegEncoderCandidate{name: "libx264", encoder: "libx264", filter: "format=yuv420p", extraArgs: []string{"-preset", "fast"}})
	}
	return &ffmpegClipEncoder{
		env:              env,
		container:        "h264",
		candidates:       candidates,
		failedCandidates: map[string]error{},
	}, nil
}

func (e *ffmpegClipEncoder) Close() error {
	e.mu.Lock()
	worker := e.worker
	e.worker = nil
	e.mu.Unlock()
	if worker != nil {
		return worker.Close()
	}
	return nil
}

func (e *ffmpegClipEncoder) QueueFrame(frame clipFrameBuffer, opts clipEncodeOptions, forceKey bool) error {
	frameSize := opts.Width * opts.Height * 4
	if frameSize <= 0 {
		return errors.New("invalid frame dimensions")
	}
	if len(frame.Buffer) < frameSize {
		return errors.New("frame buffer too small")
	}

	start := time.Now()

	worker, candidate, err := e.ensureWorker(opts, forceKey)
	if err != nil {
		recordClipEncoderEvent(ClipEncoderEvent{
			Encoder:   e.container,
			Candidate: candidate.name,
			Event:     "queue",
			Frames:    1,
			Bytes:     frameSize,
			Duration:  time.Since(start),
			Err:       err,
		})
		return err
	}

	err = worker.writeFrame(frame.Buffer[:frameSize])
	duration := time.Since(start)
	recordClipEncoderEvent(ClipEncoderEvent{
		Encoder:   e.container,
		Candidate: candidate.name,
		Event:     "queue",
		Frames:    1,
		Bytes:     frameSize,
		Duration:  duration,
		Err:       err,
	})
	if err != nil {
		e.mu.Lock()
		if e.worker == worker {
			delete(e.failedCandidates, candidate.encoder)
			e.worker = nil
		}
		e.mu.Unlock()
		worker.Close()
		return err
	}
	return nil
}

func (e *ffmpegClipEncoder) Flush(forceKey bool) (clipEncodeResult, error) {
	e.mu.Lock()
	worker := e.worker
	candidate := e.lastCandidate
	e.mu.Unlock()
	if worker == nil {
		return clipEncodeResult{}, nil
	}
	start := time.Now()
	data, err := worker.flush(forceKey)
	duration := time.Since(start)
	recordClipEncoderEvent(ClipEncoderEvent{
		Encoder:   e.container,
		Candidate: candidate.name,
		Event:     "flush",
		Frames:    0,
		Bytes:     len(data),
		Duration:  duration,
		Err:       err,
	})
	if err != nil {
		e.mu.Lock()
		if e.worker == worker {
			e.worker = nil
			e.failedCandidates[candidate.encoder] = err
		}
		e.mu.Unlock()
		worker.Close()
		return clipEncodeResult{}, err
	}
	if len(data) == 0 {
		return clipEncodeResult{}, nil
	}
	result := clipEncodeResult{
		Frames: []RemoteDesktopClipFrame{{
			OffsetMs: 0,
			Width:    worker.config.width,
			Height:   worker.config.height,
			Encoding: e.container,
			Data:     append([]byte(nil), data...),
		}},
		Bytes:       len(data),
		Encoding:    e.container,
		EncoderName: candidate.name,
	}
	return result, nil
}

func (e *ffmpegClipEncoder) ensureWorker(opts clipEncodeOptions, forceKey bool) (*ffmpegEncoderWorker, ffmpegEncoderCandidate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.env == nil {
		env, err := newFFmpegEnvironment()
		if err != nil {
			return nil, ffmpegEncoderCandidate{}, err
		}
		e.env = env
	}

	config := newClipWorkerConfig(opts)
	if forceKey && e.worker != nil {
		_ = e.worker.Close()
		e.worker = nil
	}
	if e.worker != nil && e.worker.matches(config) {
		return e.worker, e.lastCandidate, nil
	}
	if e.worker != nil {
		_ = e.worker.Close()
		e.worker = nil
	}

	var lastErr error
	for _, candidate := range e.candidates {
		if err, failed := e.failedCandidates[candidate.encoder]; failed {
			lastErr = err
			continue
		}
		worker, err := newFFmpegEncoderWorker(e.env.path, e.container, candidate, config)
		if err != nil {
			e.failedCandidates[candidate.encoder] = err
			lastErr = err
			continue
		}
		e.worker = worker
		e.lastCandidate = candidate
		delete(e.failedCandidates, candidate.encoder)
		return worker, candidate, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no encoder candidates available")
	}
	return nil, ffmpegEncoderCandidate{}, lastErr
}

func newClipWorkerConfig(opts clipEncodeOptions) clipWorkerConfig {
	fps := estimateClipFPSFromInterval(opts.FrameInterval)
	gop := clampInt(int(math.Round(fps)), 1, 300)
	if opts.ForceKey {
		gop = 1
	}
	bitrate := estimateClipBitrate(opts.Width, opts.Height, opts.Quality, opts.TargetBitrate)
	return clipWorkerConfig{
		width:        opts.Width,
		height:       opts.Height,
		bitrate:      bitrate,
		gop:          gop,
		fps:          fps,
		intraRefresh: opts.IntraRefresh,
	}
}

func estimateClipFPSFromInterval(interval time.Duration) float64 {
	if interval <= 0 {
		return 30
	}
	ms := float64(interval.Milliseconds())
	if ms <= 0 {
		return 30
	}
	fps := 1000 / ms
	if fps < 5 {
		return 5
	}
	if fps > 240 {
		return 240
	}
	return fps
}

func newFFmpegEncoderWorker(path, container string, candidate ffmpegEncoderCandidate, config clipWorkerConfig) (*ffmpegEncoderWorker, error) {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "rawvideo",
		"-pix_fmt", "bgra",
		"-video_size", fmt.Sprintf("%dx%d", config.width, config.height),
	}
	if config.fps > 0 {
		args = append(args, "-framerate", strconv.FormatFloat(config.fps, 'f', 3, 64))
	}
	args = append(args, "-i", "pipe:0")

	filter := strings.TrimSpace(candidate.filter)
	if filter == "" {
		filter = "format=yuv420p"
	}
	args = append(args, "-vf", filter)
	args = append(args, "-c:v", candidate.encoder)
	args = append(args, "-g", strconv.Itoa(config.gop))
	args = append(args, "-bf", "0")

	rate := fmt.Sprintf("%dk", config.bitrate)
	args = append(args, "-b:v", rate, "-maxrate", rate, "-bufsize", fmt.Sprintf("%dk", clampInt(config.bitrate*2, config.bitrate, 100000)))

	if len(candidate.extraArgs) > 0 {
		args = append(args, candidate.extraArgs...)
	}
	if config.intraRefresh {
		args = append(args, "-intra-refresh", "1")
	}

	container = strings.TrimSpace(container)
	if container == "" {
		container = "hevc"
	}
	args = append(args, "-f", container, "pipe:1")

	cmd := exec.Command(path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}

	worker := &ffmpegEncoderWorker{
		candidate: candidate,
		container: container,
		config:    config,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		notify:    make(chan struct{}, 1),
		parser:    &annexBNALParser{codec: container},
	}
	cmd.Stderr = &worker.stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, err
	}

	go worker.readLoop()
	return worker, nil
}

func (w *ffmpegEncoderWorker) matches(config clipWorkerConfig) bool {
	return w.config.width == config.width &&
		w.config.height == config.height &&
		w.config.bitrate == config.bitrate &&
		w.config.gop == config.gop &&
		w.config.intraRefresh == config.intraRefresh
}

func (w *ffmpegEncoderWorker) writeFrame(frame []byte) error {
	frameSize := w.config.width * w.config.height * 4
	if len(frame) < frameSize {
		return errors.New("frame buffer too small")
	}
	_, err := w.stdin.Write(frame[:frameSize])
	if err != nil {
		w.fail(err)
	}
	return err
}

func (w *ffmpegEncoderWorker) flush(forceKey bool) ([]byte, error) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		w.mu.Lock()
		hasData := len(w.buffer) > 0
		keyReady := !forceKey || (len(w.keyframes) > 0 && w.keyframes[0] == 0)
		closed := w.closed
		err := w.err
		if hasData && keyReady {
			data := append([]byte(nil), w.buffer...)
			w.buffer = nil
			w.keyframes = nil
			w.mu.Unlock()
			return data, nil
		}
		if closed {
			w.buffer = nil
			w.keyframes = nil
			w.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, nil
		}
		w.mu.Unlock()

		if time.Now().After(deadline) {
			w.mu.Lock()
			if !forceKey && len(w.buffer) > 0 {
				data := append([]byte(nil), w.buffer...)
				w.buffer = nil
				w.keyframes = nil
				w.mu.Unlock()
				return data, nil
			}
			err := w.err
			w.mu.Unlock()
			if err != nil {
				return nil, err
			}
			if forceKey {
				return nil, errors.New("encoder has not produced a keyframe")
			}
			return nil, errors.New("encoder flush timeout")
		}

		select {
		case <-w.notify:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (w *ffmpegEncoderWorker) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		w.mu.Unlock()
		if w.stdin != nil {
			_ = w.stdin.Close()
		}
		if w.stdout != nil {
			_ = w.stdout.Close()
		}
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		if w.cmd != nil {
			_ = w.cmd.Wait()
		}
		w.signal()
	})
	return nil
}

func (w *ffmpegEncoderWorker) signal() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *ffmpegEncoderWorker) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, err := w.stdout.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			w.mu.Lock()
			base := len(w.buffer)
			w.buffer = append(w.buffer, chunk...)
			offsets := w.parser.push(chunk)
			for _, offset := range offsets {
				w.keyframes = append(w.keyframes, base+offset)
			}
			w.mu.Unlock()
			w.signal()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				w.fail(err)
			}
			break
		}
	}
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.signal()
}

func (w *ffmpegEncoderWorker) fail(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
	w.signal()
}

func (p *annexBNALParser) push(chunk []byte) []int {
	if len(chunk) == 0 {
		return nil
	}
	data := append(p.tail, chunk...)
	prevTailLen := len(p.tail)
	offsets := make([]int, 0, 1)
	i := 0
	for i <= len(data)-4 {
		startLen := 0
		if data[i] == 0 && data[i+1] == 0 {
			if data[i+2] == 1 {
				startLen = 3
			} else if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
				startLen = 4
			}
		}
		if startLen > 0 {
			start := i + startLen
			if start < len(data) {
				if isKeyframeNAL(p.codec, data[start:]) {
					offset := start - prevTailLen
					if offset < 0 {
						offset = 0
					}
					offsets = append(offsets, offset)
				}
			}
			i = start
			continue
		}
		i++
	}
	if len(data) > 4 {
		p.tail = append([]byte(nil), data[len(data)-4:]...)
	} else {
		p.tail = append([]byte(nil), data...)
	}
	return offsets
}

func isKeyframeNAL(codec string, nal []byte) bool {
	if len(nal) == 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(codec))
	switch normalized {
	case remoteClipEncodingH264, "avc":
		nalType := nal[0] & 0x1F
		return nalType == 5
	case remoteClipEncodingHEVC, "h265":
		if len(nal) < 2 {
			return false
		}
		nalType := (nal[0] >> 1) & 0x3F
		return nalType >= 16 && nalType <= 21
	default:
		return false
	}
}

type ffmpegEncoderCapabilities struct {
	encoders map[string]struct{}
}

func (c *ffmpegEncoderCapabilities) supports(name string) bool {
	if c == nil {
		return false
	}
	if c.encoders == nil {
		return false
	}
	_, ok := c.encoders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

var ffmpegEncoderCache sync.Map

func detectFFmpegEncoderCapabilities(path string) *ffmpegEncoderCapabilities {
	if cached, ok := ffmpegEncoderCache.Load(path); ok {
		if caps, valid := cached.(*ffmpegEncoderCapabilities); valid && caps != nil {
			return caps
		}
	}
	caps := &ffmpegEncoderCapabilities{encoders: map[string]struct{}{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-loglevel", "error", "-encoders")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		ffmpegEncoderCache.Store(path, caps)
		return caps
	}
	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "-Encoders") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(fields[1]))
		if name != "" {
			caps.encoders[name] = struct{}{}
		}
	}
	ffmpegEncoderCache.Store(path, caps)
	return caps
}

func estimateClipFPS(frames []clipFrameBuffer, interval time.Duration) float64 {
	if len(frames) <= 1 {
		if interval <= 0 {
			return 30
		}
		ms := float64(interval.Milliseconds())
		if ms <= 0 {
			return 30
		}
		return 1000 / ms
	}
	durationMs := frames[len(frames)-1].OffsetMs - frames[0].OffsetMs
	if durationMs <= 0 {
		if interval <= 0 {
			return 30
		}
		ms := float64(interval.Milliseconds())
		if ms <= 0 {
			return 30
		}
		return 1000 / ms
	}
	return float64(len(frames)-1) * 1000 / float64(durationMs)
}

func estimateClipBitrate(width, height, quality, target int) int {
	if target > 0 {
		return clampInt(target, 600, 40000)
	}
	pixels := width * height
	base := 1500
	switch {
	case pixels >= 3840*2160:
		base = 18000
	case pixels >= 2560*1440:
		base = 11000
	case pixels >= 1920*1080:
		base = 6000
	case pixels >= 1280*720:
		base = 3500
	case pixels >= 1024*768:
		base = 2500
	default:
		base = 1500
	}
	quality = clampInt(quality, minClipQuality, maxClipQuality)
	scale := float64(quality) / float64(defaultClipQuality)
	scale = math.Max(0.5, math.Min(1.6, scale))
	bitrate := int(float64(base) * scale)
	return clampInt(bitrate, 800, 40000)
}

func encodeClipFramesJPEG(frames []clipFrameBuffer, quality int) ([]RemoteDesktopClipFrame, int, error) {
	if len(frames) == 0 {
		return nil, 0, errors.New("no frames available")
	}
	encoded := make([]RemoteDesktopClipFrame, len(frames))
	total := 0
	for idx, frame := range frames {
		data, err := encodeJPEG(frame.Width, frame.Height, quality, frame.Buffer)
		if err != nil {
			return nil, 0, err
		}
		encoded[idx] = RemoteDesktopClipFrame{
			OffsetMs: frame.OffsetMs,
			Width:    frame.Width,
			Height:   frame.Height,
			Encoding: remoteClipEncodingJPEG,
			Data:     data,
		}
		total += len(data)
	}
	return encoded, total, nil
}
